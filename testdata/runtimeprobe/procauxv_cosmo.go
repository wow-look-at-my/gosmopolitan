// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"fmt"
	"os"
	"unsafe"
)

// checkProcAuxv asserts that /proc/self/auxv reads back the same auxiliary
// vector the runtime holds, on every host.
//
// A library written for Linux reads the vector out of this file rather
// than out of the runtime. golang.org/x/sys/cpu is the one that matters:
// it asks the runtime first, but the init that arms that call runs after
// the init that makes it, so the file is the path it really takes. Where
// the read fails, its arm64 fallback reads the ID_AA64ISAR registers, and
// macOS answers that MRS with SIGILL - the program dies before main.
func checkProcAuxv() {
	buf, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		fail("procauxv", "read: %v", err)
		return
	}
	want := getAuxv()
	size := int(unsafe.Sizeof(uintptr(0)))
	// The file ends in an AT_NULL pair the runtime's slice leaves out.
	if len(buf) != (len(want)+2)*size {
		fail("procauxv", "got %d bytes, want %d for %d runtime entries", len(buf), (len(want)+2)*size, len(want))
		return
	}
	for i, v := range want {
		var got uintptr
		for b := size - 1; b >= 0; b-- {
			got = got<<8 | uintptr(buf[i*size+b])
		}
		if got != v {
			fail("procauxv", "entry %d is %#x, runtime says %#x", i, got, v)
			return
		}
	}
	ok("procauxv", fmt.Sprintf("%d entries", len(want)/2))
}
