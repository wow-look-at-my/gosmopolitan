// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"bufio"
	"bytes"
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
	"strings"
	"sync"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/lockedfile"
	"cmd/go/internal/modfetch"
	"cmd/go/internal/str"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// A module zip carries no generated file, and a submodule's contents are not
// in it either. So a dependency that generates part of its own API ships a
// package the compiler reads as empty, and every consumer fails on a symbol
// that the package's source never declares.
//
// A package that carries a directive is generated in a sandbox, and the compiler
// reads the tree the generator left.
const generatePrefix = "//go:generate"

// Dir answers the directory to read a package from: the copy carrying its
// generated files, or dir unchanged.
//
// Only a dependency is ever generated. The main module's own tree is the
// developer's to run `go generate` in, and GOROOT is not ours to write to.
func Dir(dir, modroot string) string {
	if !generateDeps() || modroot == "" || dir == "" {
		return dir
	}
	if !str.HasFilePathPrefix(dir, cfg.GOMODCACHE) {
		return dir
	}
	runnable, skip := directives(dir)
	if runnable == 0 {
		// Every directive names a program this host cannot start. That is the
		// author's own workflow, run before the module was published, and the
		// zip carries what it wrote. klauspost/compress ships a stringer
		// directive and the file stringer produced.
		return dir
	}
	rel, err := filepath.Rel(modroot, dir)
	if err != nil {
		return dir
	}
	out, err := generateModule(modroot, rel, skip)
	if err != nil {
		// A host that cannot confine a generator cannot generate anything, for
		// any module. Building past that hands every consumer a package whose
		// generated half is missing, and one of those panics when something
		// finally asks it for what it never generated.
		if sandboxUnavailable(err) {
			base.Fatalf("go: generating %s: %v", dir, err)
		}
		// A directive can be unrunnable rather than broken. A module zip drops
		// every path the go command ignores, `_codegen` among them, so a
		// generator kept beside the package it writes is absent from what a
		// consumer fetches. testify ships one, and ships its generated files
		// too, so the build needs nothing from it. The module loader and the
		// package loader both ask, so the report goes out once.
		if _, said := reported.LoadOrStore(dir, true); !said {
			fmt.Fprintf(os.Stderr, "go: generating %s: %v\n", dir, err)
			fmt.Fprintf(os.Stderr, "go: %s builds from the tree the module zip carried\n", dir)
		}
		return dir
	}
	return out
}

// reported holds the directories whose failed generation this process has
// reported.
var reported sync.Map

// directives reads a package's generate directives and sorts them by whether
// this host can start their program: the count of those it can, and a -skip
// expression naming the others. A directive whose program is not on PATH is
// one the module's author runs, not one a consumer can. It reads lines rather
// than parsing: this runs for every dependency package, ahead of the build.
//
// A -command alias is resolved within its file, as `go generate` resolves it.
// A program given as a path, or through an environment variable, is left to
// `go generate` to resolve, and counts as runnable.
func directives(dir string) (runnable int, skip string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, ""
	}
	var unrunnable []string
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") {
			continue
		}
		open, err := os.Open(filepath.Join(dir, ent.Name()))
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

// generateDeps reports whether a dependency may generate. The environment
// turns it off for a build that must read exactly what it fetched.
//
// A bootstrap cmd/go links the BOOTSTRAP toolchain's internal/cfg, which knows
// nothing of this variable and panics on the name. So ask whether the name is
// known before reading it that way.
func generateDeps() bool {
	return os.Getenv("GOGENERATEDEPS") != "off"
}

// generateModule answers the directory of package pkgrel in its module's
// generated tree, generating that package into the tree when it is not there
// yet.
//
// The tree lives in the module cache, beside the module it comes from, so it is
// cached and shared exactly like every other fetched thing. It is a sibling of
// the extracted module rather than the extracted module itself: go.sum pins the
// bytes the proxy served, `go mod verify` hashes that tree against it, and a
// generated file inside it would report every module as modified.
//
// A module has one tree, and each package the build loads from it is generated
// into it on its own. A build that imports three packages of a module needs all
// three generated, whichever of them it happened to load first.
func generateModule(modroot, pkgrel, skip string) (string, error) {
	rel, err := filepath.Rel(cfg.GOMODCACHE, modroot)
	if err != nil {
		return "", err
	}
	root := filepath.Join(cfg.GOMODCACHE, "cache", "generate", rel)
	if err := os.MkdirAll(filepath.Dir(root), 0o777); err != nil {
		return "", err
	}

	// One build generates, and every other waits for it rather than writing
	// the same tree underneath it.
	unlock, err := lockedfile.MutexAt(root + ".lock").Lock()
	if err != nil {
		return "", err
	}
	defer unlock()

	// A package directory never contains '@', so no marker can collide with
	// the directory of a package nested below the one it describes.
	marks := filepath.Join(root+".packages", pkgrel)
	done := filepath.Join(marks, "@generated")
	if _, err := os.Stat(done); err == nil {
		return filepath.Join(root, pkgrel), nil
	}
	// A module version is fixed bytes, so a directive that cannot run against it
	// cannot run against it tomorrow either. Recording that answer keeps every
	// later build from copying the tree and failing the same way. It is recorded
	// against the package whose directive failed, so a sibling that generates
	// cleanly is still generated.
	failed := filepath.Join(marks, "@failed")
	if why, err := os.ReadFile(failed); err == nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(why)))
	}

	// The generator runs in a fresh copy of the fetched module, never in the
	// tree other builds are compiling from. What it wrote reaches that tree
	// only once it has succeeded.
	stage := root + ".stage"
	if err := modfetch.RemoveAll(stage); err != nil {
		return "", err
	}
	if err := copyTree(modroot, stage); err != nil {
		modfetch.RemoveAll(stage)
		return "", err
	}
	synthesized, err := giveGoMod(stage, rel)
	if err != nil {
		modfetch.RemoveAll(stage)
		return "", err
	}
	if err := runGenerate(stage, pkgrel, skip); err != nil {
		// A half-generated package is worse than none: it compiles against
		// files the generator had not finished writing.
		modfetch.RemoveAll(stage)
		// Only a verdict about the module's own bytes may be recorded. A host
		// that lacks the sandbox says nothing about this module, and writing
		// that down makes installing the sandbox change nothing.
		if !sandboxUnavailable(err) {
			if os.MkdirAll(marks, 0o777) == nil {
				os.WriteFile(failed, []byte(err.Error()), 0o666)
			}
		}
		return "", err
	}
	// The tree carries what the module and its generators wrote, so the go.mod
	// written to make the stage a main module does not reach it.
	if synthesized {
		if err := os.Remove(filepath.Join(stage, "go.mod")); err != nil {
			modfetch.RemoveAll(stage)
			return "", err
		}
	}
	if err := publishGenerated(modroot, stage, root); err != nil {
		modfetch.RemoveAll(stage)
		return "", err
	}
	if err := os.MkdirAll(marks, 0o777); err != nil {
		return "", err
	}
	if err := os.WriteFile(done, nil, 0o666); err != nil {
		return "", err
	}
	return filepath.Join(root, pkgrel), nil
}

// giveGoMod writes stage a go.mod when the fetched module carries none, and
// reports whether it did. Without one, `go generate` in stage takes whatever
// go.mod lies above the module cache, or none, as the main module. modrel is
// the module's directory under the module cache: escaped path '@' version.
func giveGoMod(stage, modrel string) (bool, error) {
	gomod := filepath.Join(stage, "go.mod")
	_, err := os.Stat(gomod)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	escaped, _, found := strings.Cut(filepath.ToSlash(modrel), "@")
	if !found {
		return false, fmt.Errorf("%s names no module version", modrel)
	}
	modPath, err := module.UnescapePath(escaped)
	if err != nil {
		return false, err
	}
	body := "module " + modfile.AutoQuote(modPath) + "\n"
	if err := os.WriteFile(gomod, []byte(body), 0o666); err != nil {
		return false, err
	}
	return true, nil
}

// publishGenerated moves what a generator did in stage into root, the tree
// builds read. The first package of a module becomes root whole. A later one
// brings only the files its generator wrote, changed or removed relative to
// the fetched module in modroot, so the packages already generated into root
// keep what they have.
func publishGenerated(modroot, stage, root string) error {
	_, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(stage, root); err != nil {
			return err
		}
		sealTree(root)
		return nil
	}
	if err != nil {
		return err
	}

	wrote, removed, err := generatorChanges(modroot, stage)
	if err != nil {
		return err
	}
	makeTreeWritable(root)
	// root goes back to read-only whatever happens below: a tree left writable
	// is one any later build can scribble on.
	defer sealTree(root)
	for _, rel := range wrote {
		if err := placeFile(filepath.Join(stage, rel), filepath.Join(root, rel)); err != nil {
			return err
		}
	}
	for _, rel := range removed {
		if err := os.Remove(filepath.Join(root, rel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return modfetch.RemoveAll(stage)
}

// generatorChanges compares stage, a copy of modroot a generator has run in,
// against modroot. It answers the regular files the generator wrote or changed
// and the ones it removed, each relative to the tree's root.
func generatorChanges(modroot, stage string) (wrote, removed []string, err error) {
	err = filepath.WalkDir(stage, func(path string, ent fs.DirEntry, err error) error {
		if err != nil || !ent.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		same, err := sameContent(path, filepath.Join(modroot, rel))
		if err != nil {
			return err
		}
		if !same {
			wrote = append(wrote, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	err = filepath.WalkDir(modroot, func(path string, ent fs.DirEntry, err error) error {
		if err != nil || !ent.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(modroot, path)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(stage, rel)); errors.Is(err, fs.ErrNotExist) {
			removed = append(removed, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return wrote, removed, nil
}

// sameContent reports whether regular file path holds exactly the bytes of
// base. A base that does not exist holds nothing path could match.
func sameContent(path, base string) (bool, error) {
	baseInfo, err := os.Stat(base)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if !baseInfo.Mode().IsRegular() || info.Size() != baseInfo.Size() {
		return false, nil
	}
	left, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer left.Close()
	right, err := os.Open(base)
	if err != nil {
		return false, err
	}
	defer right.Close()

	leftBuf := make([]byte, 64<<10)
	rightBuf := make([]byte, 64<<10)
	for {
		num, leftErr := io.ReadFull(left, leftBuf)
		_, rightErr := io.ReadFull(right, rightBuf[:num])
		if rightErr != nil && num > 0 {
			return false, rightErr
		}
		if !bytes.Equal(leftBuf[:num], rightBuf[:num]) {
			return false, nil
		}
		if leftErr == io.EOF || leftErr == io.ErrUnexpectedEOF {
			return true, nil
		}
		if leftErr != nil {
			return false, leftErr
		}
	}
}

// placeFile copies src over dst by renaming a finished copy into place, so a
// build reading dst sees either the old file or the new one and never a part.
func placeFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "@place-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	tmp.Close()
	if err := copyFile(src, tmpName); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// sealTree makes a generated tree read-only, as the rest of the module cache
// is: what it holds is a build input like any other.
func sealTree(root string) {
	if !cfg.ModCacheRW {
		makeTreeReadOnly(root)
	}
}

// makeTreeReadOnly drops write permission on dir and everything under it,
// children before parents.
func makeTreeReadOnly(dir string) {
	var dirs []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	for idx := len(dirs) - 1; idx >= 0; idx-- {
		if info, err := os.Stat(dirs[idx]); err == nil {
			os.Chmod(dirs[idx], info.Mode()&^0o222)
		}
	}
}

// makeTreeWritable gives the owner write permission on dir and every directory
// under it, so another package's generated files can be added to the tree.
func makeTreeWritable(dir string) {
	filepath.WalkDir(dir, func(path string, ent fs.DirEntry, err error) error {
		if err != nil || !ent.IsDir() {
			return nil
		}
		if info, err := os.Stat(path); err == nil {
			os.Chmod(path, info.Mode()|0o200)
		}
		return nil
	})
}

// runGenerate runs `go generate` for one package of the generated tree.
//
// A directive is a command a dependency's author wrote, and a build runs it
// without anybody reading it first. So it runs confined: it may write the tree
// it generates and the caches a go command needs, and nothing else. The network
// stays reachable, because a generator that fetches its own inputs is the case
// this exists for. skip names the directives this host cannot start, in the
// -skip form.
func runGenerate(root, pkgrel, skip string) error {
	goCmd, err := base.GoCommand()
	if err != nil {
		return err
	}
	args := append(slices.Clone(goCmd), "generate")
	if skip != "" {
		args = append(args, "-skip="+skip)
	}
	argv, err := sandboxArgv(root, append(args, "./"+filepath.ToSlash(pkgrel))...)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	// The output streams as it always did, and a copy of the tail rides the
	// error. The verdict below is recorded once and replayed by every later
	// build, so an error that is only "exit status 1" tells the build after
	// this one nothing about why the generator stopped.
	said := &tailWriter{limit: generateTailBytes}
	cmd.Stdout = io.MultiWriter(os.Stderr, said)
	cmd.Stderr = cmd.Stdout
	// A generator is a program of this module, so it builds against the same
	// toolchain rather than fetching another one. It runs on this machine, so
	// `go generate` and every go command a directive starts build an APE,
	// which runs here whatever the build targets and is the only target a go
	// command carrying its standard library can build. Every target reads
	// that single generated tree.
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

// generateTailBytes bounds what rides the error. The verdict is a file in the
// module cache, and a generator can print a whole build log.
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
