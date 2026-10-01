// run

//go:build !nacl && !js && !wasip1 && gc

// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Check for compile or link error.

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	dir, err := os.MkdirTemp("", "issue9862")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	out, err := build(filepath.Join(dir, "issue9862.exe"), "fixedbugs/issue9862.go")
	outstr := string(out)
	if err == nil {
		println("building issue9862.go succeeded, should have failed\n", outstr)
		return
	}
	if !strings.Contains(outstr, "symbol too large") {
		println("building issue9862.go gave unexpected error; want symbol too large:\n", outstr)
	}
}

// build compiles the Go files as package main and links them into exe,
// against the standard library cmd/internal/testdir lists in
// STDLIB_IMPORTCFG. It answers what the compiler and linker printed.
func build(exe string, files ...string) ([]byte, error) {
	importcfg := os.Getenv("STDLIB_IMPORTCFG")
	if importcfg == "" {
		return nil, errors.New("STDLIB_IMPORTCFG is not set")
	}
	obj := exe + ".a"
	compile := append([]string{"tool", "compile", "-p=main", "-importcfg=" + importcfg, "-o", obj}, files...)
	if out, err := exec.Command("go", compile...).CombinedOutput(); err != nil {
		return out, err
	}
	return exec.Command("go", "tool", "link", "-importcfg="+importcfg, "-o", exe, obj).CombinedOutput()
}
