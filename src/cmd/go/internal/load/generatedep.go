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
// A package states what it generates:
//
//	//go:generate:produces parser.gen.go
//	//go:generate go run example.com/cmd/gen -out parser.gen.go input.c
//
// When a named file is absent, this copies the whole module out of the read-only
// module cache and runs the package's own directives there. The compiler then
// reads the copy. A package that names nothing, or that already carries what it
// names, costs one directory read.
const producesPrefix = "//go:generate:produces"

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
	produces := producedFiles(dir)
	if len(produces) == 0 || allPresent(dir, produces) {
		return dir
	}
	rel, err := filepath.Rel(modroot, dir)
	if err != nil {
		return dir
	}
	out, err := generateModule(modroot, rel)
	if err != nil {
		// The compiler's own "undefined" error is the honest report when the
		// repair could not run. Name the reason rather than fail the whole
		// build here: a package can be loaded and never compiled.
		fmt.Fprintf(os.Stderr, "go: could not generate %s: %v\n", dir, err)
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

// producedFiles reads the files a package's directives claim to write.
func producedFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var produces []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		produces = append(produces, scanProduces(filepath.Join(dir, e.Name()))...)
	}
	return produces
}

// scanProduces reads one file's produces directives. It reads lines rather
// than parsing: this runs for every dependency package, ahead of the build.
func scanProduces(file string) []string {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()

	var produces []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if !strings.HasPrefix(line, producesPrefix) {
			continue
		}
		for _, name := range strings.Fields(line[len(producesPrefix):]) {
			// A produced file is a name inside the package, never a path out
			// of it: this decides what gets written.
			if name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) || name == ".." {
				continue
			}
			produces = append(produces, name)
		}
	}
	return produces
}

// allPresent reports whether every produced file is already there.
func allPresent(dir string, produces []string) bool {
	for _, name := range produces {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
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
	if err := modfetch.RemoveAll(root); err != nil {
		return "", err
	}
	if err := copyTree(modroot, root); err != nil {
		return "", err
	}
	if err := runGenerate(root, pkgrel); err != nil {
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
	for i := len(dirs) - 1; i >= 0; i-- {
		if info, err := os.Stat(dirs[i]); err == nil {
			os.Chmod(dirs[i], info.Mode()&^0o222)
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
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	info, err := r.Stat()
	if err != nil {
		return err
	}
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

