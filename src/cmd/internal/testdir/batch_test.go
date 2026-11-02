// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package testdir_test

import _ "embed"

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"internal/goversion"
)

// The run corpus is a few standalone programs, each `package main` with `func
// main`, so they differ only in a name. This compiles them ONCE, as one
// package each under a generated dispatcher, and each test then runs that one
// executable with its own name. As the argument.

// batchOutput answers what one test program printed. The whole corpus runs in
// one process per CPU on the first call. This is because a process start
// costs about a second on a wasm runtime and most of these programs do almost
// nothing.
func batchOutput(corpus, file string) (out []byte, batched bool, err error) {
	exe, name, err := batchFor(corpus, file)
	if err != nil || name == "" {
		return nil, false, err
	}
	theBatch.ranOnce.Do(func() { theBatch.runAll(exe) })
	out, done := theBatch.ran[name]
	return out, done, nil
}

// launch answers the command that starts a program built here. A cross
// target's binary does not run on this host, so it starts through the exec
// wrapper the distribution ships for the port.
func launch(exe string, names ...string) []string {
	argv := []string{exe}
	if goos != runtime.GOOS || goarch != runtime.GOARCH {
		argv = append([]string{fmt.Sprintf("go_%s_%s_exec", goos, goarch)}, argv...)
	}
	return append(argv, names...)
}

// runAll runs every batched program, one process per CPU, and splits what each
// process printed.
func (bat *batch) runAll(exe string) {
	bat.ran = map[string][]byte{}

	names := make([]string, 0, len(bat.ids))
	for _, pkgname := range bat.ids {
		names = append(names, pkgname)
	}
	sort.Strings(names)

	procs := min(runtime.NumCPU(), len(names))
	shares := make([][]string, procs)
	for idx, name := range names {
		shares[idx%procs] = append(shares[idx%procs], name)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, share := range shares {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := bat.runShare(exe, share)
			mu.Lock()
			maps.Copy(bat.ran, got)
			mu.Unlock()
		}()
	}
	wg.Wait()
	fmt.Fprintf(os.Stderr, "testdir batch: %d of %d programs finished in %d processes\n", len(bat.ran), len(names), procs)
	// A program alone prints its init output before main.
	for name, out := range bat.ran {
		if init, ok := bat.initOut[name]; ok {
			bat.ran[name] = append(slices.Clip(init), out...)
		}
	}
}

// runShare runs names in one process. A program that ends the process does
// not get an output, and a new process starts at the program after it.
func (bat *batch) runShare(exe string, names []string) map[string][]byte {
	ran := map[string][]byte{}
	for len(names) > 0 {
		out := runWatched(launch(exe, append([]string{bat.nonce}, names...)...), []byte(bat.nonce+" "), batchProgramLimit)
		finished, started := splitBatch(out, bat.nonce)
		maps.Copy(ran, finished)
		if started == "" {
			return ran
		}
		next := slices.Index(names, started)
		if next < 0 {
			return ran
		}
		names = names[next+1:]
	}
	return ran
}

// batchProgramLimit is how long one batched program may run before the batch gives up on its process.
const batchProgramLimit = 2 * time.Minute

// runWatched runs argv and answers what it printed. It ends the process when
// wait passes with no new mark in the output.
func runWatched(argv []string, mark []byte, wait time.Duration) []byte {
	out := &markWriter{mark: mark, progress: make(chan struct{}, 1)}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		// Nothing ran, so the caller has no output to go by.
		fmt.Fprintf(os.Stderr, "testdir batch: cannot start %s: %v\n", argv[0], err)
		return nil
	}
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	limit := time.NewTimer(wait)
	defer limit.Stop()
	for {
		select {
		case <-done:
			return out.bytes()
		case <-out.progress:
			limit.Reset(wait)
		case <-limit.C:
			fmt.Fprintf(os.Stderr, "testdir batch: no program finished in %v; ending the process\n", wait)
			cmd.Process.Kill()
			<-done
			return out.bytes()
		}
	}
}

// markWriter collects a process's output and signals each new mark in it.
type markWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	scanned  int
	mark     []byte
	progress chan struct{}
}

func (mw *markWriter) Write(data []byte) (int, error) {
	mw.mu.Lock()
	defer mw.mu.Unlock()
	mw.buf.Write(data)
	all := mw.buf.Bytes()
	from := max(mw.scanned-len(mw.mark)+1, 0)
	if bytes.Contains(all[from:], mw.mark) {
		select {
		case mw.progress <- struct{}{}:
		default:
		}
	}
	mw.scanned = len(all)
	return len(data), nil
}

func (mw *markWriter) bytes() []byte {
	mw.mu.Lock()
	defer mw.mu.Unlock()
	return bytes.Clone(mw.buf.Bytes())
}

// splitBatch splits what one dispatcher process printed. A program's output
// runs from its own marker to the next marker. A program with no marker after
// it did not finish: splitBatch leaves it out of finished and names it in
// started. The output of a process that ended normally ends with the marker
// ".", and started is then "".
func splitBatch(out []byte, nonce string) (finished map[string][]byte, started string) {
	finished = map[string][]byte{}
	marker := []byte(nonce + " ")
	for rest := out; ; {
		start := bytes.Index(rest, marker)
		if start < 0 {
			return finished, started
		}
		rest = rest[start+len(marker):]
		line := rest
		if eol := bytes.IndexByte(rest, '\n'); eol >= 0 {
			line, rest = rest[:eol], rest[eol+1:]
		} else {
			rest = nil
		}
		name := string(line)
		if name == "." {
			return finished, ""
		}
		next := bytes.Index(rest, marker)
		if next < 0 {
			return finished, name
		}
		finished[name] = rest[:next]
	}
}

// batchFor answers the dispatcher and the name this test file takes inside
// it. The file is named the way the runner names it, relative to the corpus
// root.
func batchFor(corpus, file string) (exe, name string, err error) {
	theBatch.once.Do(func() { theBatch.build(corpus) })
	if theBatch.err != nil {
		return "", "", theBatch.err
	}
	return theBatch.exe, theBatch.ids[file], nil
}

var theBatch = &batch{ids: map[string]string{}}

// program is one test program's place in the dispatcher.
type program struct{ ID string }

// dispatch is what the dispatcher template is rendered from.
type dispatch struct {
	Programs []program
}

// The dispatcher runs every name it is given, in one process, and marks each's
// output with the nonce that is its first argument. A program that panics or
// exits takes the process with it, so the caller re-runs what never reported.
// The nonce is an argument and not a constant, so builds of the corpus give the
// same bytes. A wasm runtime then compiles the module one time, not once per
// run.
var dispatcher = template.Must(template.New("main").Parse(`package main

import (
	"fmt"
	"os"
{{range .Programs}}
	{{.ID}} "testdirbatch/{{.ID}}"
{{- end}}
)

func run(name string) {
	// The program under test reads its own argument list, and the names this
	// dispatcher took are not part of it.
	os.Args = os.Args[:1]
	switch name {
{{- range .Programs}}
	case "{{.ID}}":
		{{.ID}}.Main()
{{- end}}
	default:
		fmt.Fprintf(os.Stderr, "batch: no test %q\n", name)
		os.Exit(2)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: batch <nonce> [test...]")
		os.Exit(2)
	}
	nonce, names := os.Args[1], os.Args[2:]
	all := append([]string{os.Args[0]}, names...)
	for _, name := range names {
		fmt.Printf("%s %s\n", nonce, name)
		os.Stdout.Sync()
		os.Args = all
		run(name)
	}
	fmt.Printf("%s .\n", nonce)
}
`))

type batch struct {
	once  sync.Once
	dir   string
	exe   string
	nonce string
	ids   map[string]string
	err   error

	// initOut holds what each program prints during its package initialization.
	initOut map[string][]byte

	// ran holds each program's output from the one process that ran them all.
	ranOnce sync.Once
	ran     map[string][]byte
}

// eligible reports whether a test file can join the batch, and the source to
// put in it. A file carrying a build constraint is left out. The constraint
// decides whether the file exists at all, and a batch that loses one has a
// package with nothing in it.
func eligible(src string) (string, bool) {
	if strings.Contains(src, "//go:build") || strings.Contains(src, "// +build") {
		return "", false
	}
	// The first line names the action, the same way test.run reads it.
	first, _, _ := strings.Cut(src, "\n")
	if strings.TrimSpace(strings.TrimPrefix(first, "//")) != "run" {
		return "", false
	}
	if !strings.Contains(src, "\npackage main\n") {
		return "", false
	}
	if !strings.Contains(src, "\nfunc main() {") {
		return "", false
	}
	return src, true
}

// rewrite turns one test program into a package the dispatcher can call.
func rewrite(src, pkgname string) string {
	src = strings.Replace(src, "\npackage main\n", "\npackage "+pkgname+"\n", 1)
	// The dispatcher does not inline Main, as no caller inlines a main.
	return strings.Replace(src, "\nfunc main() {", rewriteCpp, 1)
}

func (bat *batch) build(corpus string) {
	dir, err := os.MkdirTemp("", "testdir-batch-")
	if err != nil {
		bat.err = err
		return
	}
	bat.dir = dir

	// The corpus is the same set of directories the runner walks.
	var rels []string
	for _, sub := range dirs {
		found, err := filepath.Glob(filepath.Join(corpus, sub, "*.go"))
		if err != nil {
			bat.err = err
			return
		}
		for _, path := range found {
			rels = append(rels, filepath.Join(sub, filepath.Base(path)))
		}
	}

	var programs []program
	count := 0
	for _, rel := range rels {
		file := filepath.Join(corpus, rel)
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		src, joins := eligible(string(raw))
		if !joins {
			continue
		}
		pkgname := fmt.Sprintf("prog%d", count)
		pkgdir := filepath.Join(dir, pkgname)
		if err := os.MkdirAll(pkgdir, 0o755); err != nil {
			bat.err = err
			return
		}
		if err := os.WriteFile(filepath.Join(pkgdir, filepath.Base(file)), []byte(rewrite(src, pkgname)), 0o644); err != nil {
			bat.err = err
			return
		}
		if err := os.WriteFile(filepath.Join(pkgdir, initMarkFile), []byte(initMark(pkgname)), 0o644); err != nil {
			bat.err = err
			return
		}
		programs = append(programs, program{ID: pkgname})
		bat.ids[rel] = pkgname
		count++
	}
	if count == 0 {
		bat.err = fmt.Errorf("testdir batch: no test program qualified")
		return
	}

	// The corpus is the distribution's own source, so it builds under the distribution's own language version.
	gomod := fmt.Sprintf("module testdirbatch\n\ngo 1.%d\n", goversion.Version)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		bat.err = err
		return
	}

	// A test program prints whatever it likes.
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err != nil {
		bat.err = err
		return
	}
	bat.nonce = "testdir-batch-" + hex.EncodeToString(seed[:])

	bld := &builder{work: dir, srcTrim: dir + "=>testdirbatch", run: func(env []string, args ...string) ([]byte, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		return cmd.CombinedOutput()
	}}
	ctxt, err := bld.context()
	if err != nil {
		bat.err = err
		return
	}
	goVersion := fmt.Sprintf("1.%d", goversion.Version)
	lang := "go" + langVersion(goVersion)

	// Some programs do not build as a package: one declares a name that a dot import also gives it, for example.
	var units, kept []*unit
	for _, prog := range programs {
		importPath := "testdirbatch/" + prog.ID
		pkg, err := ctxt.ImportDir(filepath.Join(dir, prog.ID), 0)
		if err != nil {
			continue
		}
		units = append(units, &unit{importPath: importPath, pkgPath: importPath, pkg: pkg, gcflags: bld.flags.allList})
	}
	bld.buildProgram(units, lang, "", "")
	var joined []program
	for idx, unit := range units {
		if unit.err == nil {
			kept = append(kept, unit)
			joined = append(joined, program{ID: path.Base(units[idx].importPath)})
		}
	}
	if len(kept) < count {
		fmt.Fprintf(os.Stderr, "testdir batch: %d of %d run programs do not build as a package, and each runs alone\n", count-len(kept), count)
		for rel, pkgname := range bat.ids {
			if !slices.ContainsFunc(joined, func(prog program) bool { return prog.ID == pkgname }) {
				delete(bat.ids, rel)
			}
		}
	}
	if len(kept) == 0 {
		bat.err = fmt.Errorf("testdir batch: none of %d programs builds as a package", count)
		return
	}

	// Every process of the batch runs the package initialization of every program in it.
	exe := filepath.Join(dir, "batch.exe")
	for {
		var main strings.Builder
		if err := dispatcher.Execute(&main, dispatch{Programs: joined}); err != nil {
			bat.err = err
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main.String()), 0o644); err != nil {
			bat.err = err
			return
		}
		mainPkg, err := ctxt.ImportDir(dir, 0)
		if err != nil {
			bat.err = err
			return
		}
		mainUnit := &unit{importPath: "testdirbatch", pkgPath: "main", pkg: mainPkg, gcflags: bld.flags.mainGcflags, deps: kept}
		godebug := defaultGODEBUG(goVersion, mainPkg.Directives, bld.fips())
		if out, err := bld.buildProgram(append(slices.Clip(kept), mainUnit), lang, godebug, exe); err != nil {
			bat.err = fmt.Errorf("testdir batch: building %d programs: %v\n%s", len(kept), err, out)
			return
		}

		out := runWatched(launch(exe, bat.nonce), []byte(bat.nonce+"i "), batchProgramLimit)
		inits, last, finished := splitInits(out, bat.nonce)
		if finished {
			bat.initOut = inits
			break
		}
		drop := slices.IndexFunc(joined, func(prog program) bool { return prog.ID == last })
		if drop < 0 {
			if len(inits) == 0 && last == "" {
				// Not one mark came back, so no program is to blame: the dispatcher does not start on this port.
				fmt.Fprintf(os.Stderr, "testdir batch: the dispatcher printed nothing, and each program runs alone\n%s", out)
				bat.exe, bat.ids = "", nil
				return
			}
			bat.err = fmt.Errorf("testdir batch: the dispatcher ended before main, and no program's initialization was under way\n%s", out)
			return
		}
		fmt.Fprintf(os.Stderr, "testdir batch: %s ends its process during package initialization, and runs alone\n", last)
		joined = slices.Delete(joined, drop, drop+1)
		kept = slices.Delete(kept, drop, drop+1)
		for rel, pkgname := range bat.ids {
			if pkgname == last {
				delete(bat.ids, rel)
			}
		}
		if len(kept) == 0 {
			bat.err = fmt.Errorf("testdir batch: no program survives its package initialization in a shared process")
			return
		}
	}
	bat.exe = exe
}

// initMarkFile is the first file of each batched program.
const initMarkFile = "0batchinit.go"

// initMark is the source of initMarkFile for the package pkgname. It prints
// the nonce, the letter i and pkgname before the package's own initialization
// runs. A program alone prints its init output before main, and the mark lets
// the batch put that output in front of the program's batched output.
func initMark(pkgname string) string {
	return fmt.Sprintf(`package %[1]s

import (
	batchfmt "fmt"
	batchos "os"
)

var _ = testdirBatchInitMark()

func testdirBatchInitMark() int {
	if len(batchos.Args) > 1 {
		batchfmt.Printf("%%si %%s\n", batchos.Args[1], %[1]q)
		batchos.Stdout.Sync()
	}
	return 0
}
`, pkgname)
}

// splitInits splits what a dispatcher run with no programs printed. inits holds
// each program's init output. last names the program whose initialization ran
// last. finished reports whether the process reached main.
func splitInits(out []byte, nonce string) (inits map[string][]byte, last string, finished bool) {
	inits = map[string][]byte{}
	mark := []byte(nonce + "i ")
	end := []byte(nonce + " .\n")
	if idx := bytes.Index(out, end); idx >= 0 {
		out, finished = out[:idx], true
	}
	for rest := out; ; {
		start := bytes.Index(rest, mark)
		if start < 0 {
			return inits, last, finished
		}
		rest = rest[start+len(mark):]
		line := rest
		if eol := bytes.IndexByte(rest, '\n'); eol >= 0 {
			line, rest = rest[:eol], rest[eol+1:]
		} else {
			rest = nil
		}
		last = string(line)
		body := rest
		if next := bytes.Index(rest, mark); next >= 0 {
			body = rest[:next]
		}
		if len(body) > 0 {
			inits[last] = body
		}
	}
}

//go:embed testdata/rewrite.cpp
var rewriteCpp string
