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
	"errors"
	"fmt"
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
	"unicode"

	"cmd/go/internal/base"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

const generatePrefix = "//go:generate"

// Complete runs the generate directives of the module extracted at modroot,
// with the module's path mod, and adds the files they wrote into modroot. It
// answers the added files, in slash form relative to modroot and sorted, or
// nothing when the module carries no directive at all.
//
// Each package that carries a directive generates on its own, in path order. A
// directive that fails stops the build. The module asked for that file, so a
// build that continues without it is compiling a module nobody wrote. What it
// reports is the symbol the missing file defines, named at the first line that
// uses it, which is nowhere near the generator that never ran.
//
// Two directives are the exception, and the answer is partial when either is
// skipped. One names a program this machine lacks. The other writes into a
// submodule's directory, which no zip of the parent carries. A partial answer
// is short a file the module's own repository has, so a caller keeps it out of
// any store the fleet reads.
func Complete(modroot, mod string, pkgs []string) (added []string, partial bool, err error) {
	if len(pkgs) == 0 {
		return nil, false, nil
	}
	stage := modroot + ".generate"
	if err := removeAll(stage); err != nil {
		return nil, false, err
	}
	defer removeAll(stage)
	// The generator runs in a fresh copy of the fetched module, never in the
	// tree other builds are compiling from. What it wrote reaches that tree
	// only once it has succeeded.
	if err := copyTree(modroot, stage); err != nil {
		return nil, false, err
	}
	synthesized, err := giveGoMod(stage, mod)
	if err != nil {
		return nil, false, err
	}
	// What a failed run wrote goes back out. A half-generated package compiles
	// against files its generator never finished, which is worse than the
	// package the zip carried.
	kept, err := additions(modroot, stage)
	if err != nil {
		return nil, false, err
	}
	for _, pkg := range pkgs {
		if gone := generatorNotShipped(stage, pkg); gone != "" {
			fmt.Fprintf(os.Stderr, "go: %s in %s names %s, which its module zip does not carry\n", mod, pkg, gone)
			continue
		}
		err := runGenerate(stage, pkg)
		grown, addErr := additions(modroot, stage)
		if addErr != nil {
			return nil, false, addErr
		}
		if err == nil {
			kept = grown
			continue
		}
		if dropErr := dropAppeared(stage, grown, kept); dropErr != nil {
			return nil, false, dropErr
		}
		// A program this machine lacks says nothing about the module, and the
		// modules that name stringer or yy ship what those write. So the
		// directive is skipped and the answer is marked partial, which keeps it
		// out of the cache the fleet reads. A machine that has the program
		// stores the whole answer.
		if programMissing(err) {
			fmt.Fprintf(os.Stderr, "go: %s in %s: %v\n", mod, pkg, err)
			partial = true
			continue
		}
		// A submodule's contents are not in the parent's zip, so a directive
		// that writes into one names a directory this copy cannot hold.
		// x/crypto's x509roots writes fallback/bundle.go, and fallback is a
		// submodule. Its own module carries that file, so a consumer of the
		// parent wants nothing from the run. The answer is marked partial,
		// which keeps a tree short of that file out of the cache the fleet
		// reads, and the module and the path are named on the way past.
		if gone := wroteNowhere(stage, pkg, err); gone != "" {
			fmt.Fprintf(os.Stderr, "go: %s in %s writes %s, and its module zip carries no directory above it\n", mod, pkg, gone)
			partial = true
			continue
		}
		// Nothing generates on a host with no sandbox, or one that cannot start
		// what the go command built. Both name a machine to fix.
		if hostCannotGenerate(err) {
			return nil, false, fmt.Errorf("this host cannot generate %s in %s: %w", mod, pkg, err)
		}
		// What is left is a generator of the module that ran and failed. A
		// module ships what its directives write, so the tree the zip carries
		// is what its authors published, and the run is a refresh of it.
		// x/text's own generators read the Unicode tables off the network and
		// write into x/net, so no consumer completes that module, and every
		// build whose graph reaches it stopped here.
		//
		// The failure is named and the answer is partial, which keeps the tree
		// out of the cache the fleet reads. A module that truly owed the file
		// fails the build right after, at the symbol it never declared, with
		// this line above it.
		fmt.Fprintf(os.Stderr, "go: generating %s in %s: %v\n", mod, pkg, err)
		fmt.Fprintf(os.Stderr, "go: %s keeps what its own zip carries for %s\n", mod, pkg)
		partial = true
	}
	if synthesized {
		if err := os.Remove(filepath.Join(stage, "go.mod")); err != nil {
			return nil, false, err
		}
	}
	added, err = additions(modroot, stage)
	if err != nil {
		return nil, false, err
	}
	for _, rel := range added {
		from := filepath.Join(stage, filepath.FromSlash(rel))
		if err := copyFile(from, filepath.Join(modroot, filepath.FromSlash(rel))); err != nil {
			return nil, false, err
		}
	}
	return added, partial, nil
}

// dropAppeared removes from stage the files grown holds and kept does not.
// Both are sorted, and both name files relative to the stage root.
func dropAppeared(stage string, grown, kept []string) error {
	had := make(map[string]bool, len(kept))
	for _, rel := range kept {
		had[rel] = true
	}
	for _, rel := range grown {
		if had[rel] {
			continue
		}
		if err := os.Remove(filepath.Join(stage, filepath.FromSlash(rel))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
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
		err := fileLines(file, func(line []byte) bool {
			directive, ok := directiveLine(line)
			if !ok {
				return true
			}
			words := strings.Fields(directive[len(generatePrefix):])
			if len(words) == 0 || words[0] == "-command" {
				return true
			}
			count++
			return true
		})
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		// A read that stops short leaves the directives under the break
		// uncounted.
		if err != nil {
			base.Fatalf("go: reading %s: %v", file, err)
		}
	}
	return count
}

// generatorNotShipped answers the path a directive of pkg names that the module
// does not carry, or "" when every path it names is present.
//
// The go command drops a directory whose name opens with an underscore from a
// module zip, so a generator kept beside the package it writes reaches no
// consumer. testify ships one at _codegen. Running the directive is impossible
// for anyone who fetched the module, so the module ships what that directive
// writes as well, and a consumer needs nothing from it.
//
// This asks what the module carries rather than what a run did. A directive
// that CAN run and fails is the module's own defect, and it stops the build.
func generatorNotShipped(stage, pkg string) string {
	dir := filepath.Join(stage, filepath.FromSlash(pkg))
	names, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, ent := range names {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") {
			continue
		}
		file := filepath.Join(dir, ent.Name())
		gone := ""
		err := fileLines(file, func(line []byte) bool {
			directive, ok := directiveLine(line)
			if !ok {
				return true
			}
			gone = missingDroppedPath(stage, dir, directive)
			return gone == ""
		})
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		// A read that stops short hides the rest of the directives, and a
		// dropped path among them then reads as a package with none.
		if err != nil {
			base.Fatalf("go: reading %s: %v", file, err)
		}
		if gone != "" {
			return gone
		}
	}
	return ""
}

// missingDroppedPath answers the first path in line that names an
// underscore-prefixed segment and is absent from the tree, or "".
//
// The path can sit inside a quoted shell program, so this reads runs of path
// characters rather than shell words.
func missingDroppedPath(stage, dir, line string) string {
	for _, word := range strings.FieldsFunc(line, func(r rune) bool {
		return !strings.ContainsRune("./_-", r) && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if !droppedFromZip(word) {
			continue
		}
		at := filepath.Join(dir, filepath.FromSlash(word))
		if !filepath.IsLocal(word) {
			at = filepath.Join(stage, filepath.FromSlash(strings.TrimPrefix(word, "../")))
		}
		if _, err := os.Lstat(at); errors.Is(err, fs.ErrNotExist) {
			return word
		}
	}
	return ""
}

// droppedFromZip reports whether path names a segment the go command leaves out
// of a module zip.
func droppedFromZip(path string) bool {
	for seg := range strings.SplitSeq(path, "/") {
		if len(seg) > 1 && seg[0] == '_' {
			return true
		}
	}
	return false
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
// That is the environment's own gap, not the module's, and the set of programs
// a dependency tree names has no bound: stringer and yy arrive through
// modernc.org and x/tools without either naming them to anybody. Each of those
// modules ships what its directives write, so a consumer needs none of them.
// The directive is skipped and the answer is marked partial, which keeps a
// machine's installed programs out of what the fleet reads.
func programMissing(err error) bool {
	if err == nil {
		return false
	}
	said := err.Error()
	return strings.Contains(said, "executable file not found") ||
		strings.Contains(said, exec.ErrNotFound.Error())
}

// wroteNowhere answers the path a failed generator could not open because a
// directory above it is absent from the staged copy, or "". The go command
// leaves a submodule's whole directory out of the parent's zip, so a directive
// that writes into one reports exactly this, on every machine, for every
// consumer of that module version.
//
// The generator's own message supplies the candidate and the staged tree
// decides. A path whose parent is there names something else, and a module's
// own defect stops the build.
func wroteNowhere(stage, pkg string, err error) string {
	if err == nil {
		return ""
	}
	for line := range strings.SplitSeq(err.Error(), "\n") {
		_, after, found := strings.Cut(line, "open ")
		if !found {
			continue
		}
		path, _, ok := strings.Cut(after, ": "+syscall.ENOENT.Error())
		if path == "" || !ok {
			continue
		}
		// The directive runs with the package as its directory, and a path of
		// its own leads out of the module as readily as into a directory the
		// zip drops. Neither reaches a consumer, so both read the same way.
		at := filepath.Join(stage, filepath.FromSlash(pkg), filepath.FromSlash(path))
		within, err := filepath.Rel(stage, at)
		if err != nil || !filepath.IsLocal(within) {
			return path
		}
		if _, err := os.Lstat(filepath.Dir(at)); errors.Is(err, fs.ErrNotExist) {
			return path
		}
	}
	return ""
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
	// This generate writes the module itself. The child loads that module's
	// packages out of the module cache, which is where Dir hands a package its
	// generated copy instead. Left on, the directive would write that copy and
	// the module this call is completing would keep none of it.
	cmd.Env = append(os.Environ(),
		"GOOS=cosmo",
		"GOARCH="+runtime.GOARCH,
		"GOGENERATEDEPS=off",
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
