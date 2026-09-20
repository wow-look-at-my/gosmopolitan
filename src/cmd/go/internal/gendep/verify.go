// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"slices"
	"sort"
	"strings"

	"cmd/go/internal/base"
)

// keepCompletion reports whether the files a module's generators added belong in
// the module. modroot holds the module as its zip published it, stage holds what
// the generators left, and added names the files stage has and modroot does not.
// why names what a rejected completion broke.
//
// A package that stops compiling once the added files join it compiles for
// nobody, and the module is better off exactly as published. A package that
// does not build without them is not one the completion broke, and it keeps
// them: a host that cannot build at all reads that way too.
func keepCompletion(modroot, stage, mod string, added []string, buildTree func(root string, pkgs []string) error) (keep bool, why string, err error) {
	pkgs := addedPackages(added)
	if len(pkgs) == 0 {
		return true, "", nil
	}
	// A consumer compiles the published files plus the added ones: a file both
	// trees hold keeps the zip's bytes, whatever the generator wrote over it.
	if err := copyTree(modroot, stage); err != nil {
		return false, "", err
	}
	// Complete throws the stage away, so a go.mod written here reaches nothing.
	if _, err := giveGoMod(stage, mod); err != nil {
		return false, "", err
	}
	broke := buildTree(stage, pkgs)
	if broke == nil {
		return true, "", nil
	}
	published := modroot + ".published"
	if err := removeAll(published); err != nil {
		return false, "", err
	}
	defer removeAll(published)
	if err := copyTree(modroot, published); err != nil {
		return false, "", err
	}
	if _, err := giveGoMod(published, mod); err != nil {
		return false, "", err
	}
	if buildTree(published, pkgs) != nil {
		return true, "", nil
	}
	return false, broke.Error(), nil
}

// addedPackages names the directories the added Go files land in, each relative
// to the module root in slash form and sorted. A file that is not Go source
// joins no package.
func addedPackages(added []string) []string {
	var dirs []string
	for _, rel := range added {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		dirs = append(dirs, path.Dir(rel))
	}
	sort.Strings(dirs)
	return slices.Compact(dirs)
}

// buildPackages compiles the named packages of the module tree at root, each
// named relative to root in slash form. It runs confined and against this
// toolchain, the way a generator does, and the error carries the tail of what
// the build said.
func buildPackages(root string, pkgs []string) error {
	goCmd, err := base.GoCommand()
	if err != nil {
		return err
	}
	args := append(slices.Clone(goCmd), "build")
	for _, pkg := range pkgs {
		pattern := "."
		if pkg != "." {
			pattern = "./" + pkg
		}
		args = append(args, pattern)
	}
	argv, err := sandboxArgv(root, args...)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	said := &tailWriter{limit: generateTailBytes}
	cmd.Stdout = io.MultiWriter(os.Stderr, said)
	cmd.Stderr = cmd.Stdout
	// A generator builds an APE and runs it here, and the package it wrote into
	// is read back under that same target.
	cmd.Env = append(os.Environ(),
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
