// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"bytes"
	"io"
	"os"
	"syscall"
	"testing"
)

// TestOpenAuxvMatchesTheKernelFile runs the /proc/self/auxv shim on a host
// that also has the real file, so the two can be held against each other.
// A macOS or NT host takes this path for real and has nothing to compare
// with; the runtimeprobe check covers it there.
func TestOpenAuxvMatchesTheKernelFile(t *testing.T) {
	want, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		t.Skipf("this host has no /proc/self/auxv to compare against: %v", err)
	}
	fd, err := syscall.OpenAuxvForTest(syscall.O_RDONLY | syscall.O_CLOEXEC)
	if err != nil {
		t.Fatalf("openAuxv: %v", err)
	}
	f := os.NewFile(uintptr(fd), "auxv")
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("served %d bytes, the kernel file holds %d; contents differ", len(got), len(want))
	}
}

// TestOpenAuxvRefusesToBeWritten pins the one flag check: a caller asking
// to write this file gets an error rather than a pipe it can fill.
func TestOpenAuxvRefusesToBeWritten(t *testing.T) {
	if _, err := syscall.OpenAuxvForTest(syscall.O_RDWR); err != syscall.EACCES {
		t.Errorf("openAuxv(O_RDWR) = %v, want EACCES", err)
	}
}
