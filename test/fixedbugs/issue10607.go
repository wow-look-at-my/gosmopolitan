// run

//go:build linux && !ppc64 && gc && cgo

// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that a -B option is passed through when using both internal
// and external linking mode.

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	dir, err := os.MkdirTemp("", "issue10607")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	test(dir, "internal")
	test(dir, "external") // The 'cgo' build constraint should imply that a linker is available.
}

func test(dir, linkmode string) {
	exe := filepath.Join(dir, linkmode+".exe")
	out, err := build(exe, []string{"-B=0x12345678", "-linkmode=" + linkmode}, filepath.Join("fixedbugs", "issue10607a.go"))
	if err == nil {
		out, err = exec.Command(exe).CombinedOutput()
	}
	if err != nil {
		fmt.Printf("BUG: linkmode=%s %v\n%s\n", linkmode, err, out)
		os.Exit(1)
	}
}

// build compiles the Go files as package main and links them into exe with
// the given linker flags, against the standard library cmd/internal/testdir
// lists in STDLIB_IMPORTCFG. It answers what the compiler and linker printed.
func build(exe string, ldflags []string, files ...string) ([]byte, error) {
	importcfg := os.Getenv("STDLIB_IMPORTCFG")
	if importcfg == "" {
		return nil, errors.New("STDLIB_IMPORTCFG is not set")
	}
	obj := exe + ".a"
	compile := append([]string{"tool", "compile", "-p=main", "-importcfg=" + importcfg, "-o", obj}, files...)
	if out, err := exec.Command("go", compile...).CombinedOutput(); err != nil {
		return out, err
	}
	link := append([]string{"tool", "link", "-importcfg=" + importcfg, "-o", exe}, ldflags...)
	return exec.Command("go", append(link, obj)...).CombinedOutput()
}
