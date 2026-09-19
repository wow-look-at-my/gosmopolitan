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

	// The plain build is the control. Both take the same source and the same
	// toolchain, so every header field that differs is one -flto moved, and
	// the bad one is among them.
	const cflags = "-flto -Wno-lto-type-mismatch -Wno-unknown-warning-option"
	for _, build := range []struct {
		what  string
		flags string
	}{
		{"plain", ""},
		{"lto", cflags},
	} {
		exe := filepath.Join(dir, build.what+".exe")
		cmd := exec.Command(testenv.GoToolPath(t), "build", "-o", exe, ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"CGO_CFLAGS="+build.flags,
			"CGO_CXXFLAGS="+build.flags,
			"CGO_LDFLAGS="+build.flags)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("building %s: %v\n%s", build.what, err, out)
			continue
		}
		describeLTOImage(t, build.what, exe)

		run := exec.Command(exe)
		run.Dir = dir
		out, err := run.CombinedOutput()
		if err != nil {
			t.Errorf("starting %s: %v\n%s", build.what, err, out)
			continue
		}
		if got := strings.TrimSpace(string(out)); got != "OK" {
			t.Errorf("%s printed %q, want OK", build.what, got)
		}
	}
}

// describeLTOImage logs the header fields NT reads before it starts an image.
// A file gcc left as LTO bytecode, or one it truncated, fails here rather
// than at the start, which is the difference worth seeing.
func describeLTOImage(t *testing.T, what, path string) {
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
	t.Logf("%s: %d bytes, first bytes %x", what, info.Size(), head)

	file, err := pe.Open(path)
	if err != nil {
		t.Errorf("%s is not a PE: %v", what, err)
		return
	}
	defer file.Close()
	t.Logf("%s: machine %#x, %d sections, characteristics %#x",
		what, file.FileHeader.Machine, file.FileHeader.NumberOfSections, file.FileHeader.Characteristics)
	switch opt := file.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		t.Logf("%s: pe64 subsystem %d, entry %#x, base %#x, imagesize %#x, headersize %#x, align %#x/%#x, dllcharacteristics %#x, stack %#x/%#x",
			what, opt.Subsystem, opt.AddressOfEntryPoint, opt.ImageBase, opt.SizeOfImage, opt.SizeOfHeaders,
			opt.SectionAlignment, opt.FileAlignment, opt.DllCharacteristics,
			opt.SizeOfStackReserve, opt.SizeOfStackCommit)
		// cmd/link drops the declared OS version to 6.1 when it cannot trust
		// the external linker with the load config directory. Windows reads
		// that directory only from version 10 up, so this says whether the
		// fallback fired.
		t.Logf("%s: os version %d.%d, subsystem version %d.%d",
			what, opt.MajorOperatingSystemVersion, opt.MinorOperatingSystemVersion,
			opt.MajorSubsystemVersion, opt.MinorSubsystemVersion)
		for idx, dir := range opt.DataDirectory {
			if dir.VirtualAddress != 0 || dir.Size != 0 {
				t.Logf("%s: datadir %2d vaddr %#x size %#x", what, idx, dir.VirtualAddress, dir.Size)
			}
		}
	case *pe.OptionalHeader32:
		t.Logf("%s: pe32 subsystem %d, entry %#x, imagesize %#x, headersize %#x, dllcharacteristics %#x",
			what, opt.Subsystem, opt.AddressOfEntryPoint, opt.SizeOfImage, opt.SizeOfHeaders, opt.DllCharacteristics)
	default:
		t.Logf("%s: no optional header", what)
	}
	for _, sec := range file.Sections {
		t.Logf(what+" section %-18s vaddr %#x vsize %#x raw %#x at %#x flags %#x",
			sec.Name, sec.VirtualAddress, sec.VirtualSize, sec.Size, sec.Offset, sec.Characteristics)
	}
}
