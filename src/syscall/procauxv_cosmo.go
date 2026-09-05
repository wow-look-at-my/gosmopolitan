// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// procSelfAuxv is the one /proc path this package answers itself.
const procSelfAuxv = "/proc/self/auxv"

//go:linkname runtime_getAuxv runtime.getAuxv
func runtime_getAuxv() []uintptr

// openProcSelfAuxv answers a read of /proc/self/auxv on a macOS host,
// which serves no /proc, from the vector the runtime already holds.
//
// golang.org/x/sys/cpu is why this exists. Its package init reaches
// readHWCAP before the init that assigns getAuxvFn, so the runtime
// answers nil however full its vector is, and this file is the only
// route left. Any successful read satisfies cpu. An unsuccessful one
// sends it to an MRS of ID_AA64ISAR0_EL1 that XNU traps, which killed
// every APE linking x/crypto before main.
//
// The answer rides a pipe: an auxv is a few hundred bytes, far under a
// pipe buffer, so one write fills it and closing the write end makes the
// read end report EOF. Nothing touches the filesystem.
//
// It reports ok false when it did not handle the call, so the caller
// carries on to the real openat.
func openProcSelfAuxv(path string, flags int) (fd int, err error, ok bool) {
	if path != procSelfAuxv || flags&O_ACCMODE != O_RDONLY || !cosmo.Darwin() {
		return 0, nil, false
	}
	auxv := runtime_getAuxv()
	if len(auxv) == 0 {
		// Let the real openat answer, so the caller sees the host's own
		// error rather than an empty file claiming the vector is empty.
		return 0, nil, false
	}

	var p [2]int
	if err := Pipe2(p[:], flags&O_CLOEXEC); err != nil {
		return 0, err, true
	}
	word := int(unsafe.Sizeof(uintptr(0)))
	buf := make([]byte, len(auxv)*word)
	for i, v := range auxv {
		putUintptrLE(buf[i*word:], v)
	}
	_, werr := Write(p[1], buf)
	Close(p[1])
	if werr != nil {
		Close(p[0])
		return 0, werr, true
	}
	return p[0], nil, true
}

// putUintptrLE writes one auxv word in the little-endian layout the
// kernel uses. Both architectures an APE boots on are little-endian.
func putUintptrLE(b []byte, v uintptr) {
	for i := 0; i < int(unsafe.Sizeof(v)); i++ {
		b[i] = byte(v)
		v >>= 8
	}
}
