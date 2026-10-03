// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testdir_test

import (
	"bytes"
	"errors"
	"fmt"
	"go/build"
	"internal/buildcfg"
	"internal/godebugs"
	"internal/goversion"
	"internal/platform"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"cmd/internal/quoted"
)

// A test program is built the way the go command builds it: cgo, the
// compiler, the assembler, the packer and the linker, run directly. The
// standard library comes from one `go list -export` per distinct build of it,
// which is its target, its GOEXPERIMENT, its -gcflags=all=, its tags, -race
// and -linkshared. No program costs a go command of its own.

// goEnv is what `go env` reports for the target.
var goEnv map[string]string

// stdBuild selects one build of the standard library.
type stdBuild struct {
	env        string // NUL-separated overrides of the target's environment
	gcflags    string // the -gcflags=all= value
	tags       string
	race       bool
	linkshared bool
}

// stdList is the importcfg of one std build.
type stdList struct {
	once    sync.Once
	file    string
	content string
	err     error
}

var (
	stdLists   sync.Map // stdBuild -> *stdList
	stdListSeq atomic.Int64
)

// stdImportcfg answers the importcfg naming every package of one std build,
// building it on first use.
func stdImportcfg(want stdBuild) (file, content string, err error) {
	entry, _ := stdLists.LoadOrStore(want, &stdList{})
	list := entry.(*stdList)
	list.once.Do(func() { list.file, list.content, list.err = listStd(want) })
	return list.file, list.content, list.err
}

// listStd builds one std. A package that does not build under it, such as
// runtime/cgo for a target this host has no C compiler for, is left out with a
// comment saying why, so only a program that imports it fails, as it would
// under the go command.
func listStd(want stdBuild) (string, string, error) {
	format := `{{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{else if .Error}}# {{.ImportPath}}: {{printf "%q" .Error.Err}}{{end}}`
	args := []string{"list", "-e", "-export", "-f", format}
	if want.gcflags != "" {
		args = append(args, "-gcflags=all="+want.gcflags)
	}
	if want.race {
		args = append(args, "-race")
	}
	if want.linkshared {
		args = append(args, "-linkshared")
	}
	if want.tags != "" {
		args = append(args, "-tags="+want.tags)
	}
	args = append(args, "std")
	cmd := exec.Command(goTool, args...)
	cmd.Env = append(os.Environ(), "GOENV=off", "GOFLAGS=")
	if want.env != "" {
		cmd.Env = append(cmd.Env, strings.Split(want.env, "\x00")...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.Bytes())
	}
	file := filepath.Join(tmpDir, fmt.Sprintf("importcfg.%d", stdListSeq.Add(1)))
	if err := os.WriteFile(file, output, 0o644); err != nil {
		return "", "", err
	}
	return file, string(output), nil
}

// buildFlags is what a test asks of a build beyond its source files, given in
// the go command's own flag syntax.
type buildFlags struct {
	allGcflags  string   // the value of the last -gcflags=all=, for every package
	allList     []string // allGcflags, split
	mainGcflags []string // the flags of the packages named on the command line
	ldflags     []string
	tags        string
	race        bool
}

// parseBuildFlags reads the go command build flags a test header carries.
func parseBuildFlags(args []string) (buildFlags, error) {
	var flags buildFlags
	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]
		if !strings.HasPrefix(arg, "-") {
			return flags, fmt.Errorf("build flag %q does not start with -", arg)
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg[1:], "-"), "=")
		if name == "race" {
			race := true
			if hasValue {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return flags, fmt.Errorf("-race=%s: %v", value, err)
				}
				race = parsed
			}
			flags.race = race
			continue
		}
		if !hasValue {
			idx++
			if idx == len(args) {
				return flags, fmt.Errorf("build flag %s needs a value", arg)
			}
			value = args[idx]
		}
		var err error
		switch name {
		case "gcflags":
			err = flags.setGcflags(value)
		case "ldflags":
			var pattern string
			pattern, value, err = flagPattern(value)
			if err == nil && pattern != "" && pattern != "all" {
				err = fmt.Errorf("-ldflags pattern %q: only all is supported", pattern)
			}
			if err == nil {
				flags.ldflags, err = quoted.Split(value)
			}
		case "tags":
			flags.tags = value
		default:
			err = fmt.Errorf("build flag %s is not supported", arg)
		}
		if err != nil {
			return flags, err
		}
	}
	return flags, nil
}

// setGcflags applies one -gcflags value. As in the go command, the last value
// whose pattern matches a package is the one that package gets.
func (flags *buildFlags) setGcflags(value string) error {
	pattern, value, err := flagPattern(value)
	if err != nil {
		return err
	}
	list, err := quoted.Split(value)
	if err != nil {
		return err
	}
	switch pattern {
	case "":
		flags.mainGcflags = list
	case "all":
		flags.allGcflags, flags.allList, flags.mainGcflags = value, list, list
	default:
		return fmt.Errorf("-gcflags pattern %q: only all is supported", pattern)
	}
	return nil
}

// flagPattern splits a per-package flag value into its package pattern and
// its flags. A value that starts with a dash has no pattern, and applies to
// the packages named on the command line.
func flagPattern(value string) (pattern, rest string, err error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") {
		return "", value, nil
	}
	pattern, rest, found := strings.Cut(value, "=")
	if !found {
		return "", "", fmt.Errorf("missing =<value> in <pattern>=<value>: %q", value)
	}
	return strings.TrimSpace(pattern), rest, nil
}

// builder builds the programs of one test.
type builder struct {
	flags     buildFlags
	env       []string // overrides of the target's environment: GOOS, GOARCH, GOEXPERIMENT and the like
	work      string   // where objects go
	omitDebug bool     // link without DWARF and a symbol table, as go run does
	run       func(env []string, args ...string) ([]byte, error)
}

// unit is one package of a program.
type unit struct {
	importPath string // as the go command names it
	pkgPath    string // as the compiler is told it: "main" for the program
	pkg        *build.Package
	gcflags    []string
	deps       []*unit
	objdir     string // ends in a separator
	archive    string
	out        []byte
	err        error
	done       chan struct{}
}

var errDependency = errors.New("a dependency failed to build")

// setting answers one setting of the target's environment.
func (bld *builder) setting(name string) string {
	for idx := len(bld.env) - 1; idx >= 0; idx-- {
		if key, value, found := strings.Cut(bld.env[idx], "="); found && key == name {
			return value
		}
	}
	return goEnv[name]
}

func (bld *builder) std() stdBuild {
	return stdBuild{
		env:        strings.Join(bld.env, "\x00"),
		gcflags:    bld.flags.allGcflags,
		tags:       bld.flags.tags,
		race:       bld.flags.race,
		linkshared: *linkshared,
	}
}

func (bld *builder) tool(name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(bld.setting("GOTOOLDIR"), name)
}

func (bld *builder) context() (build.Context, error) {
	ctxt := build.Default
	ctxt.GOOS = bld.setting("GOOS")
	ctxt.GOARCH = bld.setting("GOARCH")
	ctxt.GOROOT = bld.setting("GOROOT")
	ctxt.CgoEnabled = bld.setting("CGO_ENABLED") == "1"
	ctxt.BuildTags = strings.FieldsFunc(bld.flags.tags, func(char rune) bool { return char == ',' || char == ' ' })
	exp, err := buildcfg.ParseGOEXPERIMENT(ctxt.GOOS, ctxt.GOARCH, bld.setting("GOEXPERIMENT"))
	if err != nil {
		return ctxt, err
	}
	var tags []string
	for _, tag := range build.Default.ToolTags {
		if !strings.HasPrefix(tag, "goexperiment.") {
			tags = append(tags, tag)
		}
	}
	for _, name := range exp.Enabled() {
		tags = append(tags, "goexperiment."+name)
	}
	if bld.flags.race {
		tags = append(tags, "race")
	}
	ctxt.ToolTags = tags
	return ctxt, nil
}

func (bld *builder) pie() bool {
	return platform.DefaultPIE(bld.setting("GOOS"), bld.setting("GOARCH"), bld.flags.race)
}

// codegenArg is the code generation flag every compile and assembly of the
// program gets.
func (bld *builder) codegenArg() string {
	if *linkshared {
		return "-dynlink"
	}
	if bld.pie() && bld.setting("GOOS") != "windows" {
		return "-shared"
	}
	return ""
}

func (bld *builder) installSuffix() string {
	var parts []string
	if bld.flags.race {
		parts = append(parts, "race")
	}
	if *linkshared {
		parts = append(parts, "dynlink")
	}
	return strings.Join(parts, "_")
}

func (bld *builder) forcedGcflags() []string {
	var flags []string
	if arg := bld.codegenArg(); arg != "" {
		flags = append(flags, arg)
	}
	if bld.flags.race {
		flags = append(flags, "-race")
	}
	if *linkshared {
		flags = append(flags, "-linkshared")
	}
	return flags
}

func (bld *builder) asmArgs(unit *unit) []string {
	goos, goarch := bld.setting("GOOS"), bld.setting("GOARCH")
	args := []string{
		bld.tool("asm"), "-p", unit.pkgPath, "-trimpath", strings.TrimSuffix(unit.objdir, string(filepath.Separator)) + "=>",
		"-I", unit.objdir, "-I", filepath.Join(bld.setting("GOROOT"), "pkg", "include"),
		"-D", "GOOS_" + goos, "-D", "GOARCH_" + goarch,
	}
	if arg := bld.codegenArg(); arg != "" {
		args = append(args, arg)
	}
	if *linkshared {
		args = append(args, "-D=GOBUILDMODE_shared=1", "-linkshared")
	}
	switch goarch {
	case "386":
		args = append(args, "-D", "GO386_"+bld.setting("GO386"))
	case "amd64":
		args = append(args, "-D", "GOAMD64_"+bld.setting("GOAMD64"))
	case "mips", "mipsle":
		args = append(args, "-D", "GOMIPS_"+bld.setting("GOMIPS"))
	case "mips64", "mips64le":
		args = append(args, "-D", "GOMIPS64_"+bld.setting("GOMIPS64"))
	case "ppc64", "ppc64le":
		switch bld.setting("GOPPC64") {
		case "power10":
			args = append(args, "-D", "GOPPC64_power10", "-D", "GOPPC64_power9", "-D", "GOPPC64_power8")
		case "power9":
			args = append(args, "-D", "GOPPC64_power9", "-D", "GOPPC64_power8")
		default:
			args = append(args, "-D", "GOPPC64_power8")
		}
	case "riscv64":
		args = append(args, "-D", "GORISCV64_"+bld.setting("GORISCV64"))
	case "arm":
		switch goarm := bld.setting("GOARM"); {
		case strings.Contains(goarm, "7"):
			args = append(args, "-D", "GOARM_7", "-D", "GOARM_6", "-D", "GOARM_5")
		case strings.Contains(goarm, "6"):
			args = append(args, "-D", "GOARM_6", "-D", "GOARM_5")
		default:
			args = append(args, "-D", "GOARM_5")
		}
	case "arm64":
		if features, err := buildcfg.ParseGoarm64(bld.setting("GOARM64")); err == nil && features.LSE {
			args = append(args, "-D", "GOARM64_LSE")
		}
	}
	return args
}

func (bld *builder) fips() bool {
	setting := bld.setting("GOFIPS140")
	return setting != "" && setting != "off"
}

// buildFiles builds the named files as one package, which is what
// `go build file.go` and `go run file.go` build. Every file is in, whatever
// its build constraints say, and all of them are in one directory. A main
// package is linked into exe; any other is compiled, as go build does, unless
// the caller wants a program.
func (bld *builder) buildFiles(files []string, exe string, wantMain bool) ([]byte, error) {
	ctxt, err := bld.context()
	if err != nil {
		return nil, err
	}
	var dir string
	infos := make([]fs.FileInfo, 0, len(files))
	for _, file := range files {
		if dir != "" && filepath.Dir(file) != dir {
			return nil, fmt.Errorf("named files must all be in one directory; have %s and %s", dir, filepath.Dir(file))
		}
		dir = filepath.Dir(file)
		info, err := os.Stat(file)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	ctxt.UseAllFiles = true
	ctxt.ReadDir = func(string) ([]fs.FileInfo, error) { return infos, nil }
	pkg, err := ctxt.ImportDir(dir, 0)
	if err != nil {
		return nil, err
	}
	main := &unit{importPath: "command-line-arguments", pkgPath: "main", pkg: pkg, gcflags: bld.flags.mainGcflags}
	if pkg.Name != "main" {
		if wantMain {
			return nil, fmt.Errorf("package command-line-arguments is not a main package")
		}
		main.pkgPath = main.importPath
		exe = ""
	}
	local := "go1." + strconv.Itoa(goversion.Version)
	return bld.buildProgram([]*unit{main}, local, defaultGODEBUG("", pkg.Directives, bld.fips()), exe)
}

// buildModule builds the main package at the root of a module into exe, with
// every package of the module it imports: what `go run .` builds there.
func (bld *builder) buildModule(root, modPath, goVersion, exe string) ([]byte, error) {
	ctxt, err := bld.context()
	if err != nil {
		return nil, err
	}
	loaded := map[string]*unit{}
	var order []*unit
	var load func(importPath string) (*unit, error)
	load = func(importPath string) (*unit, error) {
		if found, seen := loaded[importPath]; seen {
			if found == nil {
				return nil, fmt.Errorf("import cycle through %s", importPath)
			}
			return found, nil
		}
		loaded[importPath] = nil
		rel := strings.TrimPrefix(strings.TrimPrefix(importPath, modPath), "/")
		pkg, err := ctxt.ImportDir(filepath.Join(root, filepath.FromSlash(rel)), 0)
		if err != nil {
			return nil, err
		}
		unit := &unit{importPath: importPath, pkgPath: importPath, pkg: pkg, gcflags: bld.flags.allList}
		for _, imp := range pkg.Imports {
			if imp != modPath && !strings.HasPrefix(imp, modPath+"/") {
				continue
			}
			dep, err := load(imp)
			if err != nil {
				return nil, err
			}
			unit.deps = append(unit.deps, dep)
		}
		loaded[importPath] = unit
		order = append(order, unit)
		return unit, nil
	}
	main, err := load(modPath)
	if err != nil {
		return nil, err
	}
	if main.pkg.Name != "main" {
		return nil, fmt.Errorf("package %s is not a main package", modPath)
	}
	main.pkgPath = "main"
	main.gcflags = bld.flags.mainGcflags
	return bld.buildProgram(order, "go"+langVersion(goVersion), defaultGODEBUG(goVersion, main.pkg.Directives, bld.fips()), exe)
}

// buildProgram compiles units, each after the units it imports, and links
// the last one into exe. An empty exe links nothing.
func (bld *builder) buildProgram(units []*unit, lang, godebug, exe string) ([]byte, error) {
	work, err := os.MkdirTemp(bld.work, "build-")
	if err != nil {
		return nil, err
	}
	_, stdContent, err := stdImportcfg(bld.std())
	if err != nil {
		return nil, err
	}
	var cfg strings.Builder
	cfg.WriteString(stdContent)
	for idx, unit := range units {
		unit.objdir = filepath.Join(work, fmt.Sprintf("b%03d", idx)) + string(filepath.Separator)
		if err := os.Mkdir(unit.objdir, 0o777); err != nil {
			return nil, err
		}
		unit.archive = unit.objdir + "_pkg_.a"
		unit.done = make(chan struct{})
		if idx < len(units)-1 {
			fmt.Fprintf(&cfg, "\npackagefile %s=%s", unit.importPath, unit.archive)
		}
	}
	importcfg := filepath.Join(work, "importcfg")
	if err := os.WriteFile(importcfg, []byte(cfg.String()), 0o644); err != nil {
		return nil, err
	}

	slots := make(chan struct{}, runtime.GOMAXPROCS(0))
	var group sync.WaitGroup
	for _, unit := range units {
		group.Add(1)
		go func() {
			defer group.Done()
			defer close(unit.done)
			for _, dep := range unit.deps {
				<-dep.done
				if dep.err != nil {
					unit.err = errDependency
					return
				}
			}
			slots <- struct{}{}
			unit.out, unit.err = bld.compile(unit, importcfg, lang)
			<-slots
		}()
	}
	group.Wait()

	var out bytes.Buffer
	for _, unit := range units {
		out.Write(unit.out)
		if unit.err != nil {
			return out.Bytes(), unit.err
		}
	}
	if exe == "" {
		return out.Bytes(), nil
	}
	main := units[len(units)-1]
	linked, err := bld.link(main, importcfg, godebug, exe)
	out.Write(linked)
	return out.Bytes(), err
}

// step runs one tool for a unit. Like the go command, it heads any output
// with the name of the package the tool was run for.
func (bld *builder) step(out *bytes.Buffer, unit *unit, env []string, args ...string) ([]byte, error) {
	output, err := bld.run(append(slices.Clip(bld.env), env...), args...)
	if len(output) > 0 {
		fmt.Fprintf(out, "# %s\n%s", unit.importPath, output)
	}
	return output, err
}

func (bld *builder) compile(unit *unit, importcfg, lang string) ([]byte, error) {
	pkg := unit.pkg
	var out bytes.Buffer
	if err := unsupportedFiles(pkg); err != nil {
		return nil, err
	}
	gofiles := absFiles(pkg.Dir, pkg.GoFiles)
	var objs []string
	if len(pkg.CgoFiles) > 0 {
		generated, cobjs, err := bld.cgo(&out, unit)
		if err != nil {
			return out.Bytes(), err
		}
		gofiles = append(gofiles, generated...)
		objs = append(objs, cobjs...)
	}

	asm := bld.asmArgs(unit)
	sfiles := absFiles(pkg.Dir, pkg.SFiles)
	if len(sfiles) > 0 {
		if err := os.WriteFile(unit.objdir+"go_asm.h", nil, 0o666); err != nil {
			return out.Bytes(), err
		}
		args := append(slices.Clip(asm), "-gensymabis", "-o", unit.objdir+"symabis")
		if _, err := bld.step(&out, unit, nil, append(args, sfiles...)...); err != nil {
			return out.Bytes(), err
		}
	}

	args := []string{
		bld.tool("compile"), "-o", unit.archive,
		"-trimpath", strings.TrimSuffix(unit.objdir, string(filepath.Separator)) + "=>",
		"-p", unit.pkgPath, "-lang=" + lang,
	}
	if len(sfiles) == 0 && len(pkg.CgoFiles) == 0 {
		args = append(args, "-complete")
	}
	if suffix := bld.installSuffix(); suffix != "" {
		args = append(args, "-installsuffix", suffix)
	}
	if bld.omitDebug {
		args = append(args, "-dwarf=false")
	}
	if version := bld.setting("GOVERSION"); strings.HasPrefix(version, "go1") {
		args = append(args, "-goversion", version)
	}
	args = append(args, bld.forcedGcflags()...)
	args = append(args, unit.gcflags...)
	args = append(args, "-nolocalimports", "-importcfg", importcfg, "-pack")
	if len(sfiles) > 0 {
		args = append(args, "-asmhdr", unit.objdir+"go_asm.h", "-symabis", unit.objdir+"symabis")
	}
	if _, err := bld.step(&out, unit, nil, append(args, gofiles...)...); err != nil {
		return out.Bytes(), err
	}

	for _, sfile := range sfiles {
		obj := unit.objdir + strings.TrimSuffix(filepath.Base(sfile), filepath.Ext(sfile)) + ".o"
		if _, err := bld.step(&out, unit, nil, append(slices.Clip(asm), "-o", obj, sfile)...); err != nil {
			return out.Bytes(), err
		}
		objs = append(objs, obj)
	}
	if err := packAppend(unit.archive, objs); err != nil {
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

// packAppend appends objects to an archive the compiler wrote, as the go
// command does.
func packAppend(archive string, objs []string) error {
	if len(objs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, obj := range objs {
		data, err := os.ReadFile(obj)
		if err != nil {
			return err
		}
		name := filepath.Base(obj)
		if len(name) > 16 {
			name = name[:16]
		}
		fmt.Fprintf(&buf, "%-16s%-12d%-6d%-6d%-8o%-10d`\n", name, 0, 0, 0, 0o644, len(data))
		buf.Write(data)
		if len(data)&1 != 0 {
			buf.WriteByte(0)
		}
	}
	file, err := os.OpenFile(archive, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if _, err := file.Write(buf.Bytes()); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// unsupportedFiles refuses a package holding files this builder does not
// build, rather than build the package without them.
func unsupportedFiles(pkg *build.Package) error {
	kinds := []struct {
		name  string
		files []string
	}{
		{"C++", pkg.CXXFiles}, {"Objective-C", pkg.MFiles}, {"Fortran", pkg.FFiles},
		{"SWIG", pkg.SwigFiles}, {"SWIG C++", pkg.SwigCXXFiles}, {"syso", pkg.SysoFiles},
		{"embed pattern", pkg.EmbedPatterns},
	}
	for _, kind := range kinds {
		if len(kind.files) > 0 {
			return fmt.Errorf("package %s: %s files are not built here: %s", pkg.Dir, kind.name, strings.Join(kind.files, " "))
		}
	}
	if len(pkg.CFiles) > 0 && len(pkg.CgoFiles) == 0 {
		return fmt.Errorf("package %s: C source files not allowed when not using cgo: %s", pkg.Dir, strings.Join(pkg.CFiles, " "))
	}
	return nil
}

// envFlags splits one of the CGO_*FLAGS settings.
func (bld *builder) envFlags(name string) ([]string, error) {
	flags, err := quoted.Split(bld.setting(name))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return flags, nil
}

// ccCommand is the C compiler command line prefix the go command uses.
func (bld *builder) ccCommand(incdir string) ([]string, error) {
	compiler, err := quoted.Split(bld.setting("CC"))
	if err != nil {
		return nil, fmt.Errorf("CC: %v", err)
	}
	if len(compiler) == 0 {
		return nil, errors.New("go env reports no CC")
	}
	gccflags, err := quoted.Split(bld.setting("GOGCCFLAGS"))
	if err != nil {
		return nil, fmt.Errorf("GOGCCFLAGS: %v", err)
	}
	args := append(compiler, "-I", incdir)
	return append(args, gccflags...), nil
}

// cgo runs cgo over a unit's cgo files and compiles the C it generates, the
// way the go command does. It answers the Go files to compile with the rest of
// the package and the objects to pack into its archive.
func (bld *builder) cgo(out *bytes.Buffer, unit *unit) (gofiles, objs []string, err error) {
	pkg := unit.pkg
	objdir := unit.objdir
	cppflags, err := bld.envFlags("CGO_CPPFLAGS")
	if err != nil {
		return nil, nil, err
	}
	cflags, err := bld.envFlags("CGO_CFLAGS")
	if err != nil {
		return nil, nil, err
	}
	ldflags, err := bld.envFlags("CGO_LDFLAGS")
	if err != nil {
		return nil, nil, err
	}
	cppflags = append(cppflags, pkg.CgoCPPFLAGS...)
	cflags = append(cflags, pkg.CgoCFLAGS...)
	ldflags = append(ldflags, pkg.CgoLDFLAGS...)
	cppflags = append(cppflags, "-I", objdir)

	cgofiles := absFiles(pkg.Dir, pkg.CgoFiles)
	gofiles = []string{objdir + "_cgo_gotypes.go"}
	cfiles := []string{objdir + "_cgo_export.c"}
	for _, file := range cgofiles {
		base := strings.TrimSuffix(filepath.Base(file), ".go")
		gofiles = append(gofiles, objdir+base+".cgo1.go")
		cfiles = append(cfiles, objdir+base+".cgo2.c")
	}
	cfiles = append(cfiles, absFiles(pkg.Dir, pkg.CFiles)...)

	env := []string{"TERM=dumb", "GOOS=" + bld.setting("GOOS"), "GOARCH=" + bld.setting("GOARCH"), "CC=" + bld.setting("CC")}
	args := []string{bld.tool("cgo"), "-objdir", objdir, "-importpath", unit.importPath}
	if len(ldflags) > 0 {
		quotedFlags := make([]string, len(ldflags))
		for idx, flag := range ldflags {
			quotedFlags[idx] = strconv.Quote(flag)
		}
		args = append(args, "-ldflags="+strings.Join(quotedFlags, " "))
		env = append(env, "CGO_LDFLAGS=")
	}
	args = append(args, "--")
	args = append(args, cppflags...)
	args = append(args, cflags...)
	args = append(args, cgofiles...)
	if _, err := bld.step(out, unit, env, args...); err != nil {
		return nil, nil, err
	}

	compileFlags := append(slices.Clip(cppflags), cflags...)
	for idx, file := range cfiles {
		obj := objdir + fmt.Sprintf("_x%03d.o", idx+1)
		if err := bld.cc(out, unit, compileFlags, obj, file); err != nil {
			return nil, nil, err
		}
		objs = append(objs, obj)
	}

	// The dynamic imports come from a link of the package's C. A failure of
	// that link is not an error: it leaves the package to the external linker.
	mainObj := objdir + "_cgo_main.o"
	if err := bld.cc(out, unit, compileFlags, mainObj, objdir+"_cgo_main.c"); err != nil {
		return nil, nil, err
	}
	linkFlags := ldflags
	goos, goarch := bld.setting("GOOS"), bld.setting("GOARCH")
	if (goarch == "arm" && goos == "linux") || goos == "android" {
		if !slices.Contains(linkFlags, "-no-pie") {
			linkFlags = append(slices.Clip(linkFlags), "-pie")
		}
		if slices.Contains(linkFlags, "-pie") && slices.Contains(linkFlags, "-static") {
			linkFlags = slices.DeleteFunc(slices.Clone(linkFlags), func(flag string) bool { return flag == "-static" })
		}
	}
	dynobj := objdir + "_cgo_.o"
	linkArgs, err := bld.ccCommand(pkg.Dir)
	if err != nil {
		return nil, nil, err
	}
	linkArgs = append(linkArgs, "-o", dynobj, mainObj)
	linkArgs = append(linkArgs, objs...)
	linkArgs = append(linkArgs, linkFlags...)
	if _, err := bld.run(append(slices.Clip(bld.env), "TERM=dumb"), linkArgs...); err != nil {
		marker := objdir + "dynimportfail"
		if err := os.WriteFile(marker, nil, 0o666); err != nil {
			return nil, nil, err
		}
		return gofiles, append(objs, marker), nil
	}
	importGo := objdir + "_cgo_import.go"
	if _, err := bld.step(out, unit, env, bld.tool("cgo"), "-dynpackage", pkg.Name, "-dynimport", dynobj, "-dynout", importGo); err != nil {
		return nil, nil, err
	}
	return append(gofiles, importGo), objs, nil
}

// cc compiles one C file. On a Go builder, as in the go command, a warning is
// an error.
func (bld *builder) cc(out *bytes.Buffer, unit *unit, flags []string, obj, file string) error {
	args, err := bld.ccCommand(unit.pkg.Dir)
	if err != nil {
		return err
	}
	args = append(args, flags...)
	args = append(args, "-o", obj, "-c", file)
	output, err := bld.step(out, unit, []string{"TERM=dumb"}, args...)
	if err == nil && len(output) > 0 && os.Getenv("GO_BUILDER_NAME") != "" {
		return errors.New("C compiler warning promoted to error on Go builders")
	}
	return err
}

func (bld *builder) link(main *unit, importcfg, godebug, exe string) ([]byte, error) {
	args := []string{bld.tool("link"), "-o", exe, "-importcfg", importcfg}
	if suffix := bld.installSuffix(); suffix != "" {
		args = append(args, "-installsuffix", suffix)
	}
	if bld.omitDebug {
		args = append(args, "-s", "-w")
	}
	if godebug != "" {
		args = append(args, "-X=runtime.godebugDefault="+godebug)
	}
	buildmode := "exe"
	if bld.pie() {
		buildmode = "pie"
	}
	args = append(args, "-buildmode="+buildmode)
	if bld.flags.race {
		args = append(args, "-race")
	}
	if *linkshared {
		args = append(args, "-linkshared", "-w")
	}
	args = append(args, bld.flags.ldflags...)
	hasExtld := slices.ContainsFunc(bld.flags.ldflags, func(flag string) bool {
		return flag == "-extld" || strings.HasPrefix(flag, "-extld=")
	})
	if cc := bld.setting("CC"); cc != "" && !hasExtld {
		args = append(args, "-extld="+cc)
	}
	args = append(args, main.archive)
	var out bytes.Buffer
	_, err := bld.step(&out, main, []string{"GOROOT=" + bld.setting("GOROOT")}, args...)
	return out.Bytes(), err
}

func absFiles(dir string, files []string) []string {
	abs := make([]string, len(files))
	for idx, file := range files {
		abs[idx] = filepath.Join(dir, file)
	}
	return abs
}

// langVersion is the language version of a go.mod go version: its major and
// minor numbers.
func langVersion(version string) string {
	if strings.Count(version, ".") >= 2 {
		version = version[:strings.LastIndex(version, ".")]
	}
	return version
}

// defaultGODEBUG is the GODEBUG default the go command links into a main
// package. goVersion is the version the main module declares, empty outside a
// module, where the toolchain's own version applies and changes nothing.
func defaultGODEBUG(goVersion string, directives []build.Directive, fips bool) string {
	settings := map[string]string{}
	if fips {
		settings["fips140"] = "on"
	}
	for _, directive := range directives {
		text, found := strings.CutPrefix(directive.Text, "//go:debug")
		if !found || strings.TrimSpace(text) == text {
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(text), "=")
		if !found || value == "" || (key != "default" && godebugs.Lookup(key) == nil) {
			continue
		}
		settings[key] = value
	}
	if version, found := settings["default"]; found {
		delete(settings, "default")
		goVersion = strings.TrimPrefix(version, "go")
	}
	if minor, found := strings.CutPrefix(langVersion(goVersion), "1."); found {
		if number, err := strconv.Atoi(minor); err == nil {
			defaults := map[string]string{}
			for _, info := range godebugs.All {
				if number < info.Changed {
					defaults[info.Name] = info.Old
				}
			}
			maps.Copy(defaults, settings)
			settings = defaults
		}
	}
	keys := slices.Sorted(maps.Keys(settings))
	pairs := make([]string, len(keys))
	for idx, key := range keys {
		pairs[idx] = key + "=" + settings[key]
	}
	return strings.Join(pairs, ",")
}

// splitRunArgs splits the arguments of `go run` into the files it builds, the
// leading arguments that end in .go, and the arguments the program gets.
func splitRunArgs(args []string) (files, rest []string) {
	idx := 0
	for idx < len(args) && strings.HasSuffix(args[idx], ".go") {
		idx++
	}
	return args[:idx], args[idx:]
}
