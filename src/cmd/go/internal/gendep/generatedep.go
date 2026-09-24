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
	"path/filepath"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/lockedfile"
	"cmd/go/internal/str"

	"golang.org/x/mod/module"
)

// A module zip carries no generated file, and a submodule's contents are not
// in it either. So a dependency that generates part of its own API ships a
// package the compiler reads as empty, and every consumer fails on a symbol
// that the package's source never declares.
//
// Complete, in complete.go, runs a module's directives over the whole module
// when the module is fetched. Dir is the loader's path for a package that
// reaches it without its module completed: that one package is generated in a
// sandbox, and the compiler reads the tree the generator left.

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
	// A generated tree lives under GOMODCACHE, so the build loads its packages
	// through here as well. They carry the directives the generator ran, and
	// generating them again nests one tree inside the last until the path is
	// too long for the host.
	if str.HasFilePathPrefix(dir, generateRoot()) {
		return dir
	}
	// The fetch completes a module where it stands, and Dir is for a package
	// that reaches the loader without that having happened. A completed module
	// already carries what its directives wrote, so generating a copy of it
	// would run those directives a second time.
	if completed(modroot) {
		return dir
	}
	// A directive is a command the dependency's author wrote, and running it
	// here reads it to nobody first. The org's own modules are this fleet's,
	// and every other module asks with the OptIn line in its own go.mod. The
	// fetch path applies the same gate, so both answer one module the same way.
	if !Allowed(modroot, modPath(modroot)) {
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
	scan.Buffer(nil, 1<<20)
	for scan.Scan() {
		if strings.HasPrefix(strings.TrimSpace(scan.Text()), generatePrefix) {
			return true
		}
	}
	// A line past the buffer ends the read, and a directive under it goes
	// unseen. That package then builds from a tree nothing generated.
	if err := scan.Err(); err != nil {
		base.Fatalf("go: reading %s: %v", file, err)
	}
	return false
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
// generateRoot answers the directory every generated tree sits under.
func generateRoot() string {
	return filepath.Join(cfg.GOMODCACHE, "cache", "generate")
}

// completed reports whether the fetch already completed the module at modroot.
// modfetch records a checksum for the completed directory beside the module's
// own downloads, and that file is the one answer both paths read.
//
// The name under the module cache is the escaped path the fetch wrote, so this
// reads it as it stands rather than escaping one of its own.
func completed(modroot string) bool {
	rel, err := filepath.Rel(cfg.GOMODCACHE, modroot)
	if err != nil {
		return false
	}
	escaped, version, found := strings.Cut(filepath.ToSlash(rel), "@")
	if !found {
		return false
	}
	marker := filepath.Join(cfg.GOMODCACHE, "cache", "download", filepath.FromSlash(escaped), "@v", version+".complete")
	_, err = os.Stat(marker)
	return err == nil
}

// modPath answers the module path of the extracted module at modroot. It
// answers "" for a directory the module cache does not name that way, and
// Allowed then reads the go.mod alone, which grants nothing on its own.
func modPath(modroot string) string {
	rel, err := filepath.Rel(cfg.GOMODCACHE, modroot)
	if err != nil {
		return ""
	}
	escaped, _, found := strings.Cut(filepath.ToSlash(rel), "@")
	if !found {
		return ""
	}
	path, err := module.UnescapePath(escaped)
	if err != nil {
		return ""
	}
	return path
}

func generateModule(modroot, pkgrel string) (string, error) {
	rel, err := filepath.Rel(cfg.GOMODCACHE, modroot)
	if err != nil {
		return "", err
	}
	root := filepath.Join(generateRoot(), rel)
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
	if err := removeAll(stage); err != nil {
		return "", err
	}
	if err := copyTree(modroot, stage); err != nil {
		removeAll(stage)
		return "", err
	}
	synthesized, err := giveGoMod(stage, rel)
	if err != nil {
		removeAll(stage)
		return "", err
	}
	if err := runGenerate(stage, pkgrel); err != nil {
		// A half-generated package is worse than none: it compiles against
		// files the generator had not finished writing.
		removeAll(stage)
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
			removeAll(stage)
			return "", err
		}
	}
	if err := publishGenerated(modroot, stage, root); err != nil {
		removeAll(stage)
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
	return removeAll(stage)
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
