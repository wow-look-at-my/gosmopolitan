// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/lockedfile"
	"cmd/go/internal/modfetch"
	"cmd/go/internal/str"
)

// A module zip carries no generated file, and a submodule's contents are not
// in it either. So a dependency that generates part of its own API ships a
// package the compiler reads as empty, and every consumer fails on a symbol
// that the package's source never declares.
//
// A package that carries a directive is generated in a sandbox, and the compiler
// reads the tree the generator left.
const generatePrefix = "//go:generate"

// generateDir answers the directory to read pkgPath's package from: the copy
// carrying its generated files, or dir unchanged.
//
// Only a dependency is ever generated. The main module's own tree is the
// developer's to run `go generate` in, and GOROOT is not ours to write to.
func generateDir(dir, modroot string) string {
	if !generateDeps() || modroot == "" || dir == "" {
		return dir
	}
	if !str.HasFilePathPrefix(dir, cfg.GOMODCACHE) {
		return dir
	}
	if !hasDirective(dir) {
		return dir
	}
	rel, err := filepath.Rel(modroot, dir)
	if err != nil {
		return dir
	}
	out, err := generateModule(modroot, rel)
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
		// too, so the build needs nothing from it.
		fmt.Fprintf(os.Stderr, "go: generating %s: %v\n", dir, err)
		fmt.Fprintf(os.Stderr, "go: %s builds from the tree the module zip carried\n", dir)
		return dir
	}
	return out
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

// hasDirective reports whether a package carries a generate directive. It reads
// lines rather than parsing: this runs for every dependency package, ahead of
// the build.
func hasDirective(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") {
			continue
		}
		if fileHasDirective(filepath.Join(dir, ent.Name())) {
			return true
		}
	}
	return false
}

func fileHasDirective(file string) bool {
	open, err := os.Open(file)
	if err != nil {
		return false
	}
	defer open.Close()

	scan := bufio.NewScanner(open)
	for scan.Scan() {
		if strings.HasPrefix(strings.TrimSpace(scan.Text()), generatePrefix) {
			return true
		}
	}
	return false
}

// generateModule answers the package directory of a generated module tree,
// building that tree when it is not there yet.
//
// It lives in the module cache, beside the module it comes from, so it is
// cached and shared exactly like every other fetched thing. It is a sibling of
// the extracted module rather than the extracted module itself: go.sum pins the
// bytes the proxy served, `go mod verify` hashes that tree against it, and a
// generated file inside it would report every module as modified.
func generateModule(modroot, pkgrel string) (string, error) {
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

	done := root + ".generated"
	if _, err := os.Stat(done); err == nil {
		return filepath.Join(root, pkgrel), nil
	}
	// A module version is fixed bytes, so a directive that cannot run against it
	// cannot run against it tomorrow either. Recording that answer keeps every
	// later build from copying the tree and failing the same way.
	failed := root + ".failed"
	if why, err := os.ReadFile(failed); err == nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(why)))
	}
	if err := modfetch.RemoveAll(root); err != nil {
		return "", err
	}
	if err := copyTree(modroot, root); err != nil {
		return "", err
	}
	if err := runGenerate(root, pkgrel); err != nil {
		// A half-generated tree is worse than none: it compiles against files
		// the generator had not finished writing.
		modfetch.RemoveAll(root)
		// Only a verdict about the module's own bytes may be recorded. A host
		// that lacks the sandbox says nothing about this module, and writing
		// that down makes installing the sandbox change nothing.
		if !sandboxUnavailable(err) {
			os.WriteFile(failed, []byte(err.Error()), 0o666)
		}
		return "", err
	}
	if err := os.WriteFile(done, nil, 0o666); err != nil {
		return "", err
	}
	// The module cache is read-only, and what it holds now is a build input
	// like any other.
	if !cfg.ModCacheRW {
		makeTreeReadOnly(root)
	}
	return filepath.Join(root, pkgrel), nil
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

// runGenerate runs `go generate` for one package of the generated tree.
//
// A directive is a command a dependency's author wrote, and a build runs it
// without anybody reading it first. So it runs confined: it may write the tree
// it generates and the caches a go command needs, and nothing else. The network
// stays reachable, because a generator that fetches its own inputs is the case
// this exists for.
func runGenerate(root, pkgrel string) error {
	goCmd, err := os.Executable()
	if err != nil {
		return err
	}
	argv, err := sandboxArgv(root, goCmd, "generate", "./"+filepath.ToSlash(pkgrel))
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	// A generator is a program of this module, so it builds against the same
	// toolchain rather than fetching another one.
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOGENERATEDEPS=off")
	return cmd.Run()
}

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
