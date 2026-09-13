// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testdir_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"text/template"

	"internal/goversion"
)

// The run corpus is a few hundred standalone programs, and the runner built
// one executable per program: a compile, a link and a process for each. On a
// cross target it was worse, because the fast path below wants a host target
// and a cross run therefore spent a whole `go run` per program.
//
// Every one of those programs is `package main` with `func main`, so they
// differ only in a name. This compiles them ONCE, as one package each under a
// generated dispatcher, and each test then runs that one executable with its
// own name as the argument. The corpus costs one build instead of hundreds,
// and on a wasm target the runtime compiles one module instead of hundreds.
//
// A test still gets its own process, so an exit status, a panic and a deadlock
// stay the test's own. Nothing is skipped: a program this cannot batch runs the
// way it always did.

// batchOutput answers what one test program printed. The whole corpus runs in
// ONE process on the first call, because a process start costs about a second
// on a wasm runtime and most of these programs do almost nothing.
//
// A program that panics or exits ends that process, so anything after it never
// reported. Those run again, one process each, which attributes the failure to
// the program that caused it instead of losing the rest of the corpus with it.
func batchOutput(corpus, file string) (out []byte, ok bool, err error) {
	exe, name, err := batchFor(corpus, file)
	if err != nil || name == "" {
		return nil, false, err
	}
	theBatch.ranOnce.Do(func() { theBatch.runAll(exe) })
	if out, done := theBatch.ran[name]; done {
		return out, true, nil
	}
	// The one process did not reach this program. Run it alone.
	argv := launch(exe, name)
	single, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	return single, true, err
}

// launch answers the command that starts the dispatcher. A cross target's
// binary does not run on this host: the distribution ships an exec wrapper per
// port, and `go run` reaches it through the go command. This reaches it
// directly, because the whole point here is to not spend a go command.
func launch(exe string, names ...string) []string {
	argv := []string{exe}
	if goos != runtime.GOOS || goarch != runtime.GOARCH {
		argv = append([]string{fmt.Sprintf("go_%s_%s_exec", goos, goarch)}, argv...)
	}
	return append(argv, names...)
}

// runAll runs every batched program in one process and splits what it printed.
func (b *batch) runAll(exe string) {
	b.ran = map[string][]byte{}

	names := make([]string, 0, len(b.ids))
	for _, id := range b.ids {
		names = append(names, id)
	}
	sort.Strings(names)

	argv := launch(exe, names...)
	out, _ := exec.Command(argv[0], argv[1:]...).CombinedOutput()

	// Each program's output runs from its own marker to the next one. A marker
	// the process never printed belongs to a program it never reached.
	marker := []byte(b.nonce + " ")
	for rest := out; ; {
		i := bytes.Index(rest, marker)
		if i < 0 {
			break
		}
		rest = rest[i+len(marker):]
		line := rest
		if j := bytes.IndexByte(rest, '\n'); j >= 0 {
			line, rest = rest[:j], rest[j+1:]
		} else {
			rest = nil
		}
		name := string(line)
		if name == "." {
			break
		}
		body := rest
		if j := bytes.Index(rest, marker); j >= 0 {
			body = rest[:j]
		}
		b.ran[name] = body
	}
}

// batchFor answers the dispatcher and the name this test file takes inside it.
// The file is named the way the runner names it, relative to the corpus root.
// An empty name means the batch does not carry the file, and the caller builds
// it the way it always did.
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
	Nonce    string
}

// The dispatcher runs every name it is given, in one process, and marks each
// one's output with a nonce the build picked. A program that panics or exits
// takes the process with it, so the caller re-runs what never reported.
var dispatcher = template.Must(template.New("main").Parse(`package main

import (
	"fmt"
	"os"
{{range .Programs}}
	{{.ID}} "testdirbatch/{{.ID}}"
{{- end}}
)

const nonce = "{{.Nonce}}"

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
		fmt.Fprintln(os.Stderr, "usage: batch <test>...")
		os.Exit(2)
	}
	names := os.Args[1:]
	all := os.Args
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

	// ran holds each program's output from the one process that ran them all.
	ranOnce sync.Once
	ran     map[string][]byte
}

// eligible reports whether a test file can join the batch, and the source to
// put in it. A file carrying a build constraint is left out: the constraint
// decides whether the file exists at all, and a batch that loses one has a
// package with nothing in it.
func eligible(src string) (string, bool) {
	if strings.Contains(src, "//go:build") || strings.Contains(src, "// +build") {
		return "", false
	}
	// The header runs to the first blank line and names the action. Only a
	// bare "// run" is batched: an argument or a flag is the caller asking
	// for something this does not reproduce.
	header, _, _ := strings.Cut(src, "\n\n")
	if !strings.Contains(header, "\n// run\n") && !strings.HasPrefix(header, "// run\n") {
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
func rewrite(src, id string) string {
	src = strings.Replace(src, "\npackage main\n", "\npackage "+id+"\n", 1)
	return strings.Replace(src, "\nfunc main() {", "\n// Main is this test program's own main.\nfunc Main() {", 1)
}

func (b *batch) build(corpus string) {
	dir, err := os.MkdirTemp("", "testdir-batch-")
	if err != nil {
		b.err = err
		return
	}
	b.dir = dir

	// The corpus is the same set of directories the runner walks, and each file
	// is named the way the runner names it: relative to the corpus root.
	var rels []string
	for _, d := range dirs {
		found, err := filepath.Glob(filepath.Join(corpus, d, "*.go"))
		if err != nil {
			b.err = err
			return
		}
		for _, f := range found {
			rels = append(rels, filepath.Join(d, filepath.Base(f)))
		}
	}

	var programs []program
	n := 0
	for _, rel := range rels {
		file := filepath.Join(corpus, rel)
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		src, ok := eligible(string(raw))
		if !ok {
			continue
		}
		id := fmt.Sprintf("t%d", n)
		pkgdir := filepath.Join(dir, id)
		if err := os.MkdirAll(pkgdir, 0o755); err != nil {
			b.err = err
			return
		}
		if err := os.WriteFile(filepath.Join(pkgdir, filepath.Base(file)), []byte(rewrite(src, id)), 0o644); err != nil {
			b.err = err
			return
		}
		programs = append(programs, program{ID: id})
		b.ids[rel] = id
		n++
	}
	if n == 0 {
		b.err = fmt.Errorf("testdir batch: no test program qualified")
		return
	}

	// The corpus is the distribution's own source, so it builds under the
	// distribution's own language version.
	gomod := fmt.Sprintf("module testdirbatch\n\ngo 1.%d\n", goversion.Version)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		b.err = err
		return
	}

	// A test program prints whatever it likes, so the marker separating one
	// program's output from the next is a value nothing can predict.
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err != nil {
		b.err = err
		return
	}
	b.nonce = "testdir-batch-" + hex.EncodeToString(seed[:])

	var main strings.Builder
	if err := dispatcher.Execute(&main, dispatch{Programs: programs, Nonce: b.nonce}); err != nil {
		b.err = err
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main.String()), 0o644); err != nil {
		b.err = err
		return
	}

	exe := filepath.Join(dir, "batch.exe")
	cmd := exec.Command(goTool, "build", "-o", exe, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		b.err = fmt.Errorf("testdir batch: building %d programs: %v\n%s", n, err, out)
		return
	}
	b.exe = exe
}
