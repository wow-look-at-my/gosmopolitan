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
// golang.org/x/sys/cpu is why this exists: its package init reaches
// readHWCAP before the init that assigns getAuxvFn, so the runtime
// answers nil however full its vector is. Any successful read satisfies
// cpu; an unsuccessful one sends it to an MRS of ID_AA64ISAR0_EL1 that
// XNU traps, killing an APE linking x/crypto before main.
//
// The answer rides a pipe: an auxv is far under a pipe buffer, so one
// write fills it and closing the write end reports EOF, and nothing
// touches the filesystem. ok is false when this did not handle the
// call, so the caller carries on to the real openat.
func openProcSelfAuxv(path string, flags int) (fd int, err error, ok bool) {
	if path != procSelfAuxv || flags&O_ACCMODE != O_RDONLY || !cosmo.Darwin() {
		return 0, nil, false
	}
	if len(runtime_getAuxv()) == 0 {
		// Let the real openat answer, so the caller sees the host's own
		// error rather than an empty file claiming the vector is empty.
		return 0, nil, false
	}
	fd, err = openAuxv(flags)
	return fd, err, true
}

// openAuxv serves the file, with no host test of its own. Openat calls it
// after the real open failed, which is how a host that is neither macOS nor
// Linux gets an answer: Windows serves no /proc either, and x/sys/cpu asks
// for this path there too, because GOOS=cosmo compiles its Linux port.
func openAuxv(flags int) (fd int, err error) {
	if flags&O_ACCMODE != O_RDONLY {
		return -1, EACCES
	}
	auxv := runtime_getAuxv()
	// The kernel's file ends in an AT_NULL pair. runtime.getAuxv leaves it
	// out, so a reader that stops on the terminator rather than on EOF
	// needs it put back.
	pairs := make([]uintptr, len(auxv)+2)
	copy(pairs, auxv)

	var p [2]int
	if err := Pipe2(p[:], flags&O_CLOEXEC); err != nil {
		return -1, err
	}
	word := int(unsafe.Sizeof(uintptr(0)))
	buf := make([]byte, len(pairs)*word)
	for i, v := range pairs {
		putUintptrLE(buf[i*word:], v)
	}
	_, werr := Write(p[1], buf)
	Close(p[1])
	if werr != nil {
		Close(p[0])
		return -1, werr
	}
	return p[0], nil
}

// putUintptrLE writes one auxv word in the little-endian layout the
// kernel uses. Both architectures an APE boots on are little-endian.
func putUintptrLE(b []byte, v uintptr) {
	for i := 0; i < int(unsafe.Sizeof(v)); i++ {
		b[i] = byte(v)
		v >>= 8
	}
}
