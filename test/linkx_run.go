// run

//go:build !nacl && !js && !wasip1 && gc

// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Run the linkx test.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	dir, err := os.MkdirTemp("", "linkx")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	// test(" ") // old deprecated & removed syntax
	test(dir, "=") // new syntax
}

func test(dir, sep string) {
	// Successful run
	exe := filepath.Join(dir, "linkx.exe")
	built, err := build(exe, strings.Fields("-X main.tbd"+sep+"hello -X main.overwrite"+sep+"trumped -X main.nosuchsymbol"+sep+"neverseen"), "linkx.go")
	if err != nil {
		fmt.Println(string(built))
		fmt.Println(err)
		os.Exit(1)
	}
	argv := launch(exe)
	cmd := exec.Command(argv[0], argv[1:]...)
	var out, errbuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errbuf
	err = cmd.Run()
	if err != nil {
		fmt.Println(errbuf.String())
		fmt.Println(out.String())
		fmt.Println(err)
		os.Exit(1)
	}

	want := "hello\nhello\nhello\ntrumped\ntrumped\ntrumped\n"
	got := out.String()
	if got != want {
		fmt.Printf("got %q want %q\n", got, want)
		os.Exit(1)
	}

	// Issue 8810
	_, err = build(exe, strings.Fields("-X main.tbd"), "linkx.go")
	if err == nil {
		fmt.Println("-X linker flag should not accept keys without values")
		os.Exit(1)
	}

	// Issue 9621
	outx, err := build(exe, strings.Fields("-X main.b=false -X main.x=42"), "linkx.go")
	if err == nil {
		fmt.Println("-X linker flag should not overwrite non-strings")
		os.Exit(1)
	}
	outstr := string(outx)
	if !strings.Contains(outstr, "main.b") {
		fmt.Printf("-X linker flag did not diagnose overwrite of main.b:\n%s\n", outstr)
		os.Exit(1)
	}
	if !strings.Contains(outstr, "main.x") {
		fmt.Printf("-X linker flag did not diagnose overwrite of main.x:\n%s\n", outstr)
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
