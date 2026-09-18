// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const mmapProbeBody = "mmap-probe-body\n"

// checkMmap maps a file both ways a caller does and proves the mapping
// carries the file's bytes in each direction.
//
// Linux and macOS serve mmap directly. NT has no such call, so the
// emulation builds a section over the slot's HANDLE and maps a view of
// it. Nothing else here leaves the process address space by that route,
// and github.com/wow-look-at-my/go-mmap is a first-party consumer of it.
func checkMmap() {
	s := &softStep{name: "mmap"}

	dir, err := os.MkdirTemp("", "rp-mmap")
	if !s.do("MkdirTemp", err) {
		s.finish("")
		return
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "body")
	if !s.do("WriteFile", os.WriteFile(path, []byte(mmapProbeBody), 0o644)) {
		s.finish("")
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if !s.do("OpenFile", err) {
		s.finish("")
		return
	}
	defer f.Close()

	n := len(mmapProbeBody)
	m, err := syscall.Mmap(int(f.Fd()), 0, n,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if !s.do("Mmap", err) {
		s.finish("")
		return
	}
	if got := string(m); got != mmapProbeBody {
		s.do("mapped bytes", fmt.Errorf("read %q, want %q", got, mmapProbeBody))
		syscall.Munmap(m)
		s.finish("")
		return
	}

	// A shared mapping is the file: a write through it reaches the bytes
	// on disk, which is the half an anonymous mapping cannot show.
	m[0] = 'M'
	if !s.do("Munmap", syscall.Munmap(m)) {
		s.finish("")
		return
	}
	back, err := os.ReadFile(path)
	if !s.do("ReadFile after the write", err) {
		s.finish("")
		return
	}
	if len(back) != n || back[0] != 'M' {
		s.do("write-back", fmt.Errorf("file reads %q, want the first byte replaced", string(back)))
	}

	// Anonymous memory, the other shape every caller asks for.
	a, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if !s.do("Mmap(anonymous)", err) {
		s.finish("")
		return
	}
	a[4095] = 7
	if a[4095] != 7 {
		s.do("anonymous memory", fmt.Errorf("a byte written to it did not read back"))
	}
	if !s.do("Munmap(anonymous)", syscall.Munmap(a)) {
		s.finish("")
		return
	}

	s.finish(fmt.Sprintf("%d bytes shared and 4096 anonymous", n))
}
