// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	linuxTCSETS = 0x5402
	linuxICANON = 0x2
	linuxECHO   = 0x8
	linuxVMIN   = 6
	linuxVTIME  = 5
)

// checkPty puts a real terminal into raw mode and reads the settings
// back. Until this existed the termios conversion had never met a driver:
// the tables are unit-tested arithmetic, and the runtimeprobe termios
// check only proves the request reaches the kernel.
//
// The terminal is a pty this process opens, so it needs no controlling
// terminal and runs the same on a CI runner as anywhere. Windows has no
// pty layer, so the step reports there rather than failing.
func checkPty() {
	s := &softStep{name: "pty", soft: cosmoHostOS() == "windows"}

	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if !s.do("open /dev/ptmx", err) {
		s.finish("no pty layer on this host")
		return
	}
	defer m.Close()

	var before linuxTermios
	if !s.do("TCGETS", syscall.Ioctl(int(m.Fd()), linuxTCGETS,
		uintptr(unsafe.Pointer(&before)))) {
		s.finish("")
		return
	}
	if before.Cflag == 0 && before.Lflag == 0 {
		s.do("TCGETS values", fmt.Errorf("all-zero termios from a real pty"))
		s.finish("")
		return
	}

	// Raw mode, the way every terminal library does it: clear the line
	// discipline's echo and canonical bits, then set a byte-at-a-time
	// read. VMIN and VTIME are the two c_cc slots that differ in index
	// between the systems, so a wrong table shows up here.
	raw := before
	raw.Lflag &^= linuxICANON | linuxECHO
	raw.Cc[linuxVMIN] = 1
	raw.Cc[linuxVTIME] = 0
	if !s.do("TCSETS", syscall.Ioctl(int(m.Fd()), linuxTCSETS,
		uintptr(unsafe.Pointer(&raw)))) {
		s.finish("")
		return
	}

	var after linuxTermios
	if !s.do("TCGETS after set", syscall.Ioctl(int(m.Fd()), linuxTCGETS,
		uintptr(unsafe.Pointer(&after)))) {
		s.finish("")
		return
	}
	if after.Lflag&(linuxICANON|linuxECHO) != 0 {
		s.do("raw mode", fmt.Errorf("lflag %#x still has ICANON or ECHO set", after.Lflag))
	}
	if after.Cc[linuxVMIN] != 1 {
		s.do("VMIN", fmt.Errorf("read back %d, want 1", after.Cc[linuxVMIN]))
	}
	// Every bit outside the two cleared ones must survive: the darwin
	// path writes back a converted Apple struct, and a dropped field
	// reads as a setting the caller never asked to change.
	if got, want := after.Iflag, before.Iflag; got != want {
		s.do("iflag", fmt.Errorf("changed from %#x to %#x across a set that never touched it", want, got))
	}
	if got, want := after.Cflag, before.Cflag; got != want {
		s.do("cflag", fmt.Errorf("changed from %#x to %#x across a set that never touched it", want, got))
	}

	// Put it back, so a failure here is the emulation and not the pty.
	s.do("TCSETS restore", syscall.Ioctl(int(m.Fd()), linuxTCSETS,
		uintptr(unsafe.Pointer(&before))))

	s.finish(fmt.Sprintf("raw mode on a real pty: lflag %#x -> %#x", before.Lflag, after.Lflag))
}
