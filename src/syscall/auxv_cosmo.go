// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "unsafe"

// procSelfAuxv is the one /proc path an APE has to answer for itself. A
// library written for Linux reads the auxiliary vector out of this file,
// and only a Linux host has it.
//
// golang.org/x/sys/cpu is the reader that made this necessary. It asks the
// runtime first, through a getAuxv linkname, but the assignment that arms
// that call sits in an init function of a later file, so the init that
// reads it always finds nil. The file is the path it really takes. When
// the read fails on arm64 it goes on to read the ID_AA64ISAR registers,
// an MRS that macOS answers with SIGILL, and the program dies before main.
const procSelfAuxv = "/proc/self/auxv"

//go:linkname runtimeGetAuxv runtime.getAuxv
func runtimeGetAuxv() []uintptr

// openAuxv serves procSelfAuxv out of the runtime's own auxiliary vector.
// It writes the pairs into a pipe and hands back the read end, so the
// descriptor reads and closes like any other. The vector is a few hundred
// bytes, far below a pipe buffer, so the write cannot block.
//
// The caller reaches here only after the real open failed, which keeps a
// Linux host on the kernel's own file.
func openAuxv(mode int) (fd int, err error) {
	if mode&(O_WRONLY|O_RDWR) != 0 {
		return -1, EACCES
	}
	auxv := runtimeGetAuxv()
	// The file ends in an AT_NULL pair. runtime.getAuxv leaves it out.
	pairs := make([]uintptr, len(auxv)+2)
	copy(pairs, auxv)
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&pairs[0])), len(pairs)*int(unsafe.Sizeof(uintptr(0))))

	var p [2]int
	if err := Pipe2(p[:], mode&O_CLOEXEC); err != nil {
		return -1, err
	}
	if _, err := Write(p[1], buf); err != nil {
		Close(p[0])
		Close(p[1])
		return -1, err
	}
	Close(p[1])
	return p[0], nil
}
