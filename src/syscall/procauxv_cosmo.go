// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

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

// openProcSelfAuxv answers a read of /proc/self/auxv on a macOS host, which
// serves no /proc, from the vector the runtime already holds.
func openProcSelfAuxv(path string, flags int) (fd int, err error, ok bool) {
	if path != procSelfAuxv || flags&O_ACCMODE != O_RDONLY || !cosmo.Darwin() {
		return 0, nil, false
	}
	if len(runtime_getAuxv()) == 0 {
		// Let the real openat answer.
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
	// The kernel's file ends in an AT_NULL pair. runtime.getAuxv leaves it out.
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
