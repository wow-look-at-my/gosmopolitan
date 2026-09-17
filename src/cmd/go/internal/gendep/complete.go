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
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

const generatePrefix = "//go:generate"

// Enabled reports whether a dependency may generate. The environment turns it
// off for a build that must read exactly what it fetched.
func Enabled() bool {
	return os.Getenv("GOGENERATEDEPS") != "off"
}

// Complete runs the generate directives of the module extracted at modroot,
// with the module's path mod, and adds the files they wrote into modroot. It
// answers the added files, relative to modroot and sorted, or nothing when the
// module carries no directive this host can run.
//
// A directive that fails says something about the module: its result is no
// added file, reported once, and the module builds from the zip's tree. A host
// that cannot confine a generator, or cannot start one the go command built,
// says nothing about the module and stops the build: building past it hands
// every consumer a package whose generated half is missing.
func Complete(modroot, mod string) ([]string, error) {
	if !Enabled() {
		return nil, nil
	}
	runnable, skip := moduleDirectives(modroot)
	if runnable == 0 {
		// Every directive names a program this host cannot start. That is the
		// author's own workflow, run before the module was published, and the
		// zip carries what it wrote. klauspost/compress ships a stringer
		// directive and the file stringer produced.
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
	if err := runGenerate(stage, skip); err != nil {
		if hostCannotGenerate(err) {
			return nil, err
		}
		// A directive can be unrunnable rather than broken. A module zip drops
		// every path the go command ignores, `_codegen` among them, so a
		// generator kept beside the package it writes is absent from what a
		// consumer fetches. testify ships one, and ships its generated files
		// too, so the build needs nothing from it.
		fmt.Fprintf(os.Stderr, "go: generating %s: %v\n", modroot, err)
		fmt.Fprintf(os.Stderr, "go: %s builds from the tree the module zip carried\n", modroot)
		return nil, nil
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
	for _, rel := range added {
		if err := copyFile(filepath.Join(stage, rel), filepath.Join(modroot, rel)); err != nil {
			return nil, err
		}
	}
	return added, nil
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
			added = append(added, rel)
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

// moduleDirectives reads the generate directives of every package under
// modroot, as directives does for one: the count this host can run, and a
// -skip expression naming the others.
func moduleDirectives(modroot string) (runnable int, skip string) {
	var files []string
	filepath.WalkDir(modroot, func(path string, ent fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ent.IsDir() {
			// A nested module is its own module, with its own zip.
			if path != modroot {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return fs.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(ent.Name(), ".go") {
			files = append(files, path)
		}
		return nil
	})
	return directives(files)
}

// directives reads the generate directives of the given Go files and sorts
// them by whether this host can start their program: the count of those it
// can, and a -skip expression naming the others. A directive whose program is
// not on PATH is one the module's author runs, not one a consumer can. It reads
// lines rather than parsing: this runs for every dependency, ahead of the build.
//
// A -command alias is resolved within its file, as `go generate` resolves it.
// A program given as a path, or through an environment variable, is left to
// `go generate` to resolve, and counts as runnable.
func directives(files []string) (runnable int, skip string) {
	var unrunnable []string
	for _, file := range files {
		open, err := os.Open(file)
		if err != nil {
			continue
		}
		aliases := map[string]string{}
		scan := bufio.NewScanner(open)
		scan.Buffer(nil, 1<<20)
		for scan.Scan() {
			line := strings.TrimSpace(scan.Text())
			if !strings.HasPrefix(line, generatePrefix+" ") && !strings.HasPrefix(line, generatePrefix+"\t") {
				continue
			}
			words := strings.Fields(line[len(generatePrefix):])
			if len(words) == 0 {
				continue
			}
			if words[0] == "-command" {
				if len(words) >= 3 {
					aliases[words[1]] = words[2]
				}
				continue
			}
			prog := words[0]
			if alias, ok := aliases[prog]; ok {
				prog = alias
			}
			if programRunnable(prog) {
				runnable++
				continue
			}
			unrunnable = append(unrunnable, line)
		}
		open.Close()
	}
	if len(unrunnable) == 0 {
		return runnable, ""
	}
	quoted := make([]string, len(unrunnable))
	for idx, line := range unrunnable {
		quoted[idx] = regexp.QuoteMeta(line)
	}
	return runnable, "^(?:" + strings.Join(quoted, "|") + ")$"
}

// programRunnable reports whether `go generate` could start prog here: the go
// command itself, a path, a word an environment variable expands, or a name
// found beside this go command or on PATH.
func programRunnable(prog string) bool {
	if prog == "go" || strings.HasPrefix(prog, "$") || strings.ContainsAny(prog, `/\`) {
		return true
	}
	if _, err := exec.LookPath(filepath.Join(cfg.GOROOTbin, prog)); err == nil {
		return true
	}
	_, err := exec.LookPath(prog)
	return err == nil
}

// hostCannotGenerate reports whether err is a fact about this host rather than
// about the module: the sandbox is missing, or a generator the go command
// built would not start.
func hostCannotGenerate(err error) bool {
	return sandboxUnavailable(err) || startFailed(err)
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

// runGenerate runs `go generate ./...` in root, the staged copy of a module.
//
// A directive is a command a dependency's author wrote, and a build runs it
// without anybody reading it first. So it runs confined: it may write the tree
// it generates and the caches a go command needs, and nothing else. The network
// stays reachable, because a generator that fetches its own inputs is the case
// this exists for. skip names the directives this host cannot start, in the
// -skip form.
func runGenerate(root, skip string) error {
	goCmd, err := base.GoCommand()
	if err != nil {
		return err
	}
	args := append(slices.Clone(goCmd), "generate")
	if skip != "" {
		args = append(args, "-skip="+skip)
	}
	argv, err := sandboxArgv(root, append(args, "./...")...)
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
