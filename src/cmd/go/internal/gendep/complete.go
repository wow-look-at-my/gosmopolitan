// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gendep completes a fetched module: it runs the module's own
// go:generate directives once, over the whole module, and adds what they
// wrote to the extracted tree.
//
// A module zip carries no generated file, and a submodule's contents are not
// in it either. So a dependency that generates part of its own API ships a
// package the compiler reads as empty, and every consumer fails on a symbol
// that the package's source never declares.
//
// The complete tree is the fetched module plus the files its generators added.
// A file the zip carries keeps the zip's bytes, whatever a generator wrote over
// it: what a module's authors published is the module, and a generator that
// rewrites it from what it fetches today produces a different package tomorrow.
// A build compiles the same bytes on every machine and every day, or the cache
// that shares its outputs is worth nothing.
package gendep

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"

	"cmd/go/internal/base"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

const generatePrefix = "//go:generate"

// Enabled reports whether a dependency may generate.
//
// GOGENERATEDEPS=off is what the generators themselves run under: a directive
// that starts a go command must not complete its own module again. It is the
// only setting, and it does not change what a build produces, only whether the
// generators run at all.
//
// A bootstrap cmd/go links the BOOTSTRAP toolchain's internal/cfg, which knows
// nothing of this variable and panics on the name. So it is read from the
// environment rather than through cfg.
func Enabled() bool {
	return os.Getenv("GOGENERATEDEPS") != "off"
}

// Complete runs the generate directives of the module extracted at modroot,
// with the module's path mod, and adds the files they wrote into modroot. It
// answers the added files, in slash form relative to modroot and sorted, or
// nothing when the module carries no directive at all.
//
// Each package that carries a directive generates on its own, in path order, so
// one package's broken directive costs that package alone. What a failed run
// wrote is deleted: a half-generated package compiles against files its
// generator never finished, which is worse than the empty package the zip
// carried. Every other package keeps what it wrote.
//
// A host that cannot confine a generator, cannot start one the go command built,
// or lacks a program a directive names, says nothing about the module and stops
// the build: building past it hands every consumer a package whose generated
// half is missing.
func Complete(modroot, mod string, pkgs []string) ([]string, error) {
	if !Enabled() || len(pkgs) == 0 {
		return nil, nil
	}
	stage := modroot + ".generate"
	if err := removeAll(stage); err != nil {
		return nil, err
	}
	defer removeAll(stage)
	// The generator runs in a fresh copy of the fetched module, never in the
	// tree other builds are compiling from. What it wrote reaches that tree
	// only once it has succeeded.
	if err := copyTree(modroot, stage); err != nil {
		return nil, err
	}
	synthesized, err := giveGoMod(stage, mod)
	if err != nil {
		return nil, err
	}
	kept, err := additions(modroot, stage)
	if err != nil {
		return nil, err
	}
	for _, pkg := range pkgs {
		runErr := runGenerate(stage, pkg)
		grown, err := additions(modroot, stage)
		if err != nil {
			return nil, err
		}
		if runErr == nil {
			kept = grown
			continue
		}
		if hostCannotGenerate(runErr) {
			return nil, runErr
		}
		// A module zip drops every path the go command ignores, `_codegen`
		// among them, so a generator kept beside the package it writes is
		// absent from what a consumer fetches. testify ships one, and ships
		// its generated files too, so the build needs nothing from it.
		//
		// The module's own bytes decide which package fails, so every machine
		// reaches the same tree.
		fmt.Fprintf(os.Stderr, "go: generating %s in %s: %v\n", mod, pkg, runErr)
		fmt.Fprintf(os.Stderr, "go: %s keeps what its other packages generated\n", mod)
		for _, rel := range appeared(grown, kept) {
			if err := os.Remove(filepath.Join(stage, filepath.FromSlash(rel))); err != nil {
				return nil, err
			}
		}
	}
	if synthesized {
		if err := os.Remove(filepath.Join(stage, "go.mod")); err != nil {
			return nil, err
		}
	}
	added, err := additions(modroot, stage)
	if err != nil {
		return nil, err
	}
	added = keepable(modroot, stage, mod, added)
	for _, rel := range added {
		from := filepath.Join(stage, filepath.FromSlash(rel))
		if err := copyFile(from, filepath.Join(modroot, filepath.FromSlash(rel))); err != nil {
			return nil, err
		}
	}
	return added, nil
}

// keepable answers the added files that may join the module, and reports each
// one it leaves out.
//
// A generator also writes a file the module's authors leave out on purpose,
// whose declarations the published source already makes another way. Adding it
// declares those names twice and the package stops compiling. The module as
// published decides, exactly as it does for a file both trees hold.
//
// A build constraint is not read here, so a name two files declare under
// constraints that never hold together drops the generated file too.
func keepable(modroot, stage, mod string, added []string) []string {
	kept := make([]string, 0, len(added))
	for _, rel := range added {
		name, other := clash(modroot, stage, rel)
		if name == "" {
			kept = append(kept, rel)
			continue
		}
		fmt.Fprintf(os.Stderr, "go: %s: %s and %s both declare %s: keeping what the module published\n",
			mod, rel, other, name)
	}
	return kept
}

// clash answers the package-scope name that the added file rel declares a second
// time, and the module's own file in that directory which already declares it.
// Both are empty when the file adds only new names.
func clash(modroot, stage, rel string) (name, other string) {
	if filepath.Ext(rel) != ".go" {
		return "", ""
	}
	pkg, names := declared(filepath.Join(stage, filepath.FromSlash(rel)))
	if pkg == "" || len(names) == 0 {
		return "", ""
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	dir := filepath.Dir(filepath.Join(modroot, filepath.FromSlash(rel)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") {
			continue
		}
		// A file of the same directory declaring another package is another
		// package: an external test package sits beside the one it tests.
		published, held := declared(filepath.Join(dir, ent.Name()))
		if published != pkg {
			continue
		}
		for _, name := range sorted {
			if held[name] {
				return name, ent.Name()
			}
		}
	}
	return "", ""
}

// declared answers the package a Go file belongs to and the package-scope names
// it declares. A file it cannot parse answers nothing.
//
// A method is named for its receiver, because methods collide only on the same
// type. Several init functions in one package are legal, so init is not a name.
func declared(path string) (string, map[string]bool) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return "", nil
	}
	names := make(map[string]bool)
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			name := decl.Name.Name
			if decl.Recv != nil {
				recv := receiverName(decl.Recv)
				if recv == "" {
					continue
				}
				name = recv + "." + name
			} else if name == "init" {
				continue
			}
			names[name] = true
		case *ast.GenDecl:
			if decl.Tok == token.IMPORT {
				continue
			}
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					names[spec.Name.Name] = true
				case *ast.ValueSpec:
					for _, ident := range spec.Names {
						names[ident.Name] = true
					}
				}
			}
		}
	}
	// The blank identifier declares nothing, and a package may hold many.
	delete(names, "_")
	return file.Name.Name, names
}

// receiverName answers the type name a method is declared on, through a pointer
// and through type parameters alike. It is empty for a receiver this does not
// recognize.
func receiverName(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch indexed := expr.(type) {
	case *ast.IndexExpr:
		expr = indexed.X
	case *ast.IndexListExpr:
		expr = indexed.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// additions answers the regular files under stage that modroot does not have,
// relative to the root and sorted. A file both hold is the module's, whatever
// the generator did to it.
func additions(modroot, stage string) ([]string, error) {
	var added []string
	err := filepath.WalkDir(stage, func(path string, ent fs.DirEntry, err error) error {
		if err != nil || !ent.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		_, err = os.Lstat(filepath.Join(modroot, rel))
		if errors.Is(err, fs.ErrNotExist) {
			added = append(added, filepath.ToSlash(rel))
			return nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(added)
	return added, nil
}

// appeared answers the entries of grown that kept does not have. Both are
// sorted.
func appeared(grown, kept []string) []string {
	had := make(map[string]bool, len(kept))
	for _, rel := range kept {
		had[rel] = true
	}
	var out []string
	for _, rel := range grown {
		if !had[rel] {
			out = append(out, rel)
		}
	}
	return out
}

// Packages answers the directories under modroot that carry a generate
// directive, each relative to modroot in slash form and sorted. The order is the
// module's own, so every machine generates in the same order.
//
// An empty answer means the module needs nothing, and a caller asks before it
// reaches for anything else: a module with no directive costs a build only this
// scan.
//
// A nested module is skipped. It is its own module, with its own zip, and
// `go generate` in this one never reaches it.
func Packages(modroot string) []string {
	var pkgs []string
	filepath.WalkDir(modroot, func(path string, ent fs.DirEntry, err error) error {
		if err != nil || !ent.IsDir() {
			return err
		}
		if path != modroot {
			// The go command's own rules: a directory it never matches with a
			// package pattern is one `go generate` cannot be pointed at either.
			name := ent.Name()
			if name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return fs.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return fs.SkipDir
			}
		}
		var files []string
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		for _, ent := range entries {
			if !ent.IsDir() && strings.HasSuffix(ent.Name(), ".go") {
				files = append(files, filepath.Join(path, ent.Name()))
			}
		}
		if directives(files) == 0 {
			return nil
		}
		rel, err := filepath.Rel(modroot, path)
		if err != nil {
			return nil
		}
		pkgs = append(pkgs, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(pkgs)
	return pkgs
}

// directives counts the generate directives in the given Go files. It reads
// lines rather than parsing: this runs for every dependency, ahead of the
// build.
//
// Which directives a module has is decided by the module's own bytes and by
// nothing else. A count that also asked what this machine has installed would
// make one module version mean two different things, and both would be stored
// under the one key the whole fleet reads.
func directives(files []string) int {
	count := 0
	for _, file := range files {
		open, err := os.Open(file)
		if err != nil {
			continue
		}
		scan := bufio.NewScanner(open)
		scan.Buffer(nil, 1<<20)
		for scan.Scan() {
			line := strings.TrimSpace(scan.Text())
			if !strings.HasPrefix(line, generatePrefix+" ") && !strings.HasPrefix(line, generatePrefix+"\t") {
				continue
			}
			words := strings.Fields(line[len(generatePrefix):])
			if len(words) == 0 || words[0] == "-command" {
				continue
			}
			count++
		}
		open.Close()
	}
	return count
}

// hostCannotGenerate reports whether err is a fact about this host rather than
// about the module: the sandbox is missing, a generator the go command built
// would not start, or a program a directive names is not installed.
func hostCannotGenerate(err error) bool {
	return sandboxUnavailable(err) || startFailed(err) || programMissing(err)
}

// programMissing reports whether err says a directive named a program this
// machine does not have.
//
// That is the environment's own gap, not the module's. Skipping the directive
// would hand this build a module that the same version elsewhere does not
// match, so the build stops and names what to install instead.
func programMissing(err error) bool {
	if err == nil {
		return false
	}
	said := err.Error()
	return strings.Contains(said, "executable file not found") ||
		strings.Contains(said, exec.ErrNotFound.Error())
}

// startFailed reports whether err says the host refused to start a program the
// generator built. The sandboxed go command prints the exec failure and exits,
// so what reaches this process is its exit status with that line in the tail.
// A host whose kernel cannot start an APE without help produces exactly this
// for every generator, and says nothing about any of their modules.
func startFailed(err error) bool {
	return err != nil && strings.Contains(err.Error(), syscall.ENOEXEC.Error())
}

// giveGoMod writes stage a go.mod when the fetched module carries none, and
// reports whether it did. Without one, `go generate` in stage takes whatever
// go.mod lies above the module cache, or none, as the main module.
func giveGoMod(stage, mod string) (bool, error) {
	gomod := filepath.Join(stage, "go.mod")
	_, err := os.Stat(gomod)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := module.CheckPath(mod); err != nil {
		return false, err
	}
	body := "module " + modfile.AutoQuote(mod) + "\n"
	if err := os.WriteFile(gomod, []byte(body), 0o666); err != nil {
		return false, err
	}
	return true, nil
}

// runGenerate runs `go generate` for one package of root, the staged copy of a
// module.
//
// A directive is a command a dependency's author wrote, and a build runs it
// without anybody reading it first. So it runs confined: it may write the tree
// it generates and the caches a go command needs, and nothing else. The network
// stays reachable, because a generator that fetches its own inputs is the case
// this exists for.
//
// Every directive runs. There is no -skip: what a module generates is the
// module's own business, and a machine that cannot run one of its directives is
// a machine to fix.
func runGenerate(root, pkg string) error {
	goCmd, err := base.GoCommand()
	if err != nil {
		return err
	}
	args := append(slices.Clone(goCmd), "generate")
	pattern := "."
	if pkg != "." {
		pattern = "./" + pkg
	}
	argv, err := sandboxArgv(root, append(args, pattern)...)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	// The output streams as it always did, and a copy of the tail rides the
	// error, which is what a build reports about the module.
	said := &tailWriter{limit: generateTailBytes}
	cmd.Stdout = io.MultiWriter(os.Stderr, said)
	cmd.Stderr = cmd.Stdout
	// A generator is a program of this module, so it builds against the same
	// toolchain rather than fetching another one. It runs on this machine, so
	// `go generate` and every go command a directive starts build an APE,
	// which runs here whatever the build targets and is the only target a go
	// command carrying its standard library can build. Every target reads
	// that single completed module.
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN=local",
		"GOGENERATEDEPS=off",
		"GOOS=cosmo",
		"GOARCH="+runtime.GOARCH,
	)
	err = cmd.Run()
	if err == nil {
		return nil
	}
	if tail := strings.TrimSpace(said.String()); tail != "" {
		return fmt.Errorf("%w\n%s", err, tail)
	}
	return err
}

// generateTailBytes bounds what rides the error: a generator can print a whole
// build log.
const generateTailBytes = 4 << 10

// tailWriter keeps the last limit bytes written to it and drops the rest. The
// end of a generator's output is where it says what went wrong.
type tailWriter struct {
	limit int
	buf   []byte
}

func (sink *tailWriter) Write(payload []byte) (int, error) {
	wrote := len(payload)
	if wrote > sink.limit {
		payload = payload[wrote-sink.limit:]
	}
	sink.buf = append(sink.buf, payload...)
	if over := len(sink.buf) - sink.limit; over > 0 {
		sink.buf = sink.buf[over:]
	}
	return wrote, nil
}

func (sink *tailWriter) String() string { return string(sink.buf) }

// copyTree copies src to dst, writable. The module cache is read-only, and a
// generator has to write beside the source it reads.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o777)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	from, err := os.Open(src)
	if err != nil {
		return err
	}
	defer from.Close()
	info, err := from.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
		return err
	}
	into, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(into, from); err != nil {
		into.Close()
		return err
	}
	return into.Close()
}

// removeAll removes dir, which a generator may have left read-only in part.
func removeAll(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	filepath.WalkDir(dir, func(path string, ent fs.DirEntry, err error) error {
		if err == nil && ent.IsDir() {
			os.Chmod(path, 0o777)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
