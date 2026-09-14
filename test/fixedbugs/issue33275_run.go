// run

//go:build !nacl && !js && !wasip1 && !gccgo

// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Make sure we don't get an index out of bounds error
// while trying to print a map that is concurrently modified.
// The runtime might complain (throw) if it detects the modification,
// so we have to run the test as a subprocess.

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	dir, err := os.MkdirTemp("", "issue33275")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, "issue33275.exe")
	if out, err := build(exe, "fixedbugs/issue33275.go"); err != nil {
		panic("building issue33275.go: " + err.Error() + "\n" + string(out))
	}
	argv := launch(exe)
	out, _ := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if strings.Contains(string(out), "index out of range") {
		panic(`issue33275.go reported "index out of range"`)
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

// launch answers the command that runs exe. A program built for a target
// this machine does not run directly starts through the exec wrapper the
// distribution ships for that target.
func launch(exe string) []string {
	const targetOS, targetArch = runtime.GOOS, runtime.GOARCH
	if runtime.GOOS == targetOS && runtime.GOARCH == targetArch {
		return []string{exe}
	}
	if wrapper, err := exec.LookPath("go_" + targetOS + "_" + targetArch + "_exec"); err == nil {
		return []string{wrapper, exe}
	}
	return []string{exe}
}
