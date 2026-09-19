// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package swig

import (
	"debug/pe"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The smallest program that reaches an external link: one cgo call and one
// line of output.
const ltoProbeSource = `package main

/*
#include <stdio.h>
static int answer(void) { return 42; }
*/
import "C"

func main() {
	if int(C.answer()) != 42 {
		panic("wrong answer")
	}
	println("OK")
}
`

// TestLinkUnderLTO builds a cgo program with -flto and starts it. TestCall
// and TestCallback do the same through swig, and NT refuses what comes out
// with ERROR_BAD_EXE_FORMAT.
//
// This program uses no swig and no C++, so it says which half owns the
// defect. The host is already measured: gcc links two C files under -flto
// here and the result runs (dats/test/nt-lto.ps1). So a failure here is the
// object cmd/link hands gcc.
//
// The log describes the image gcc wrote, because NT reports a bad one only
// as a number.
func TestLinkUnderLTO(t *testing.T) {
	testenv.MustHaveCGO(t)
	testenv.MustHaveGoBuild(t)
	testenv.MustHaveExec(t)

	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module ltoprobe\n\ngo 1.27\n")
	write("main.go", ltoProbeSource)

	const cflags = "-flto -Wno-lto-type-mismatch -Wno-unknown-warning-option"
	exe := filepath.Join(dir, "ltoprobe.exe")
	build := exec.Command(testenv.GoToolPath(t), "build", "-o", exe, ".")
	build.Dir = dir
	build.Env = append(os.Environ(),
		"CGO_CFLAGS="+cflags,
		"CGO_CXXFLAGS="+cflags,
		"CGO_LDFLAGS="+cflags)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building under -flto: %v\n%s", err, out)
	}
	describeLTOImage(t, exe)

	run := exec.Command(exe)
	run.Dir = dir
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("starting the -flto build: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "OK" {
		t.Errorf("the -flto build printed %q, want OK", got)
	}
}

// describeLTOImage logs the header fields NT reads before it starts an image.
// A file gcc left as LTO bytecode, or one it truncated, fails here rather
// than at the start, which is the difference worth seeing.
func describeLTOImage(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 16)
	if file, err := os.Open(path); err == nil {
		file.Read(head)
		file.Close()
	}
	t.Logf("image %s: %d bytes, first bytes %x", path, info.Size(), head)

	file, err := pe.Open(path)
	if err != nil {
		t.Errorf("the image is not a PE: %v", err)
		return
	}
	defer file.Close()
	t.Logf("machine %#x, %d sections, characteristics %#x",
		file.FileHeader.Machine, file.FileHeader.NumberOfSections, file.FileHeader.Characteristics)
	switch opt := file.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		t.Logf("pe64: subsystem %d, entry %#x, imagesize %#x, headersize %#x, dllcharacteristics %#x",
			opt.Subsystem, opt.AddressOfEntryPoint, opt.SizeOfImage, opt.SizeOfHeaders, opt.DllCharacteristics)
	case *pe.OptionalHeader32:
		t.Logf("pe32: subsystem %d, entry %#x, imagesize %#x, headersize %#x, dllcharacteristics %#x",
			opt.Subsystem, opt.AddressOfEntryPoint, opt.SizeOfImage, opt.SizeOfHeaders, opt.DllCharacteristics)
	default:
		t.Logf("no optional header")
	}
	for _, sec := range file.Sections {
		t.Logf("section %-10s vaddr %#x vsize %#x raw %#x at %#x",
			sec.Name, sec.VirtualAddress, sec.VirtualSize, sec.Size, sec.Offset)
	}
}
