// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package base

import (
	"internal/unsafeheader"
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// TODO(mdempsky): Is there a higher-level abstraction that still
// works well for iimport?

// MapFile returns length bytes from the file starting at the
// specified offset as a string.
func MapFile(f *os.File, offset, length int64) (string, error) {
	// POSIX mmap: "The implementation may require that off is a
	// multiple of the page size."
	x := offset & int64(os.Getpagesize()-1)
	offset -= x
	length += x

	buf, err := syscall.Mmap(int(f.Fd()), offset, int(length), syscall.PROT_READ, syscall.MAP_SHARED)
	runtime.KeepAlive(f)
	if err != nil {
		// An APE maps a file through whichever host it booted on, and NT
		// takes an offset at a multiple of its allocation granularity, which
		// is wider than a page. An archive inside the blob sits where it
		// sits, so the mapping is refused and a read answers the same bytes.
		return readFileRange(f, offset+x, length-x)
	}

	buf = buf[x:]
	pSlice := (*unsafeheader.Slice)(unsafe.Pointer(&buf))

	var res string
	pString := (*unsafeheader.String)(unsafe.Pointer(&res))

	pString.Data = pSlice.Data
	pString.Len = pSlice.Len

	return res, nil
}

// readFileRange answers the same bytes a mapping would, for a host that
// refuses the mapping.
func readFileRange(f *os.File, offset, length int64) (string, error) {
	buf := make([]byte, length)
	if _, err := io.ReadFull(io.NewSectionReader(f, offset, length), buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
