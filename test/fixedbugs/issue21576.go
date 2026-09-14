// run

//go:build !nacl && !js && !wasip1 && !gccgo

// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// Ensure that deadlock detection can still
// run even with an import of "_ os/signal".

package main

import (
	"bytes"
	"context"
	"errors"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const prog = `
package main

import _ "os/signal"

func main() {
  c := make(chan int)
  c <- 1
}
`

func main() {
	dir, err := ioutil.TempDir("", "21576")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	file := filepath.Join(dir, "main.go")
	if err := ioutil.WriteFile(file, []byte(prog), 0655); err != nil {
		log.Fatalf("Write error %v", err)
	}

	exe := filepath.Join(dir, "main.exe")
	if out, err := build(exe, file); err != nil {
		log.Fatalf("build failed: %v\n%s", err, out)
	}

	// Using a timeout of 1 minute in case other factors might slow
	// down the start of the program. See https://golang.org/issue/34836.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	argv := launch(exe)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		log.Fatalf("Passed, expected an error")
	}

	want := []byte("fatal error: all goroutines are asleep - deadlock!")
	if !bytes.Contains(output, want) {
		log.Fatalf("Unmatched error message %q:\nin\n%s\nError: %v", want, output, err)
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
