// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"fmt"
	"os"

	// The linkname below needs it.
	_ "unsafe"
)

// getAuxv reaches the runtime's own vector the way golang.org/x/sys/cpu
// and golang.org/x/sys/unix reach it. The runtime pins the symbol and
// its signature for those two consumers.
//
//go:linkname getAuxv runtime.getAuxv
func getAuxv() []uintptr

// AT_PAGESZ names the system page size. It is the one tag every host an
// APE boots on can answer.
const atPagesz = 6

// checkAuxv asserts the runtime published a vector on this host.
//
// An empty vector is not a harmless gap. A consumer that finds none asks
// the hardware directly, and on arm64 that is an MRS of ID_AA64ISAR0_EL1
// that only a Linux kernel emulates. Linux supplies the vector on the
// stack, the NT stub fabricates one, and on macOS the APE loader builds
// a System V stack that carries one - which is the property this check
// holds the loader to.
func checkAuxv() {
	a := getAuxv()
	if len(a) == 0 {
		fail("auxv", "empty on host %s; a reader falls back to a Linux-only CPU probe", cosmoHostOS())
		return
	}
	if len(a)%2 != 0 {
		fail("auxv", "%d elements, want tag/value pairs", len(a))
		return
	}
	want := uintptr(os.Getpagesize())
	for i := 0; i < len(a); i += 2 {
		if a[i] != atPagesz {
			continue
		}
		if a[i+1] != want {
			fail("auxv", "AT_PAGESZ %d, want the %d os.Getpagesize reports", a[i+1], want)
			return
		}
		ok("auxv", fmt.Sprintf("pagesz=%d pairs=%d", want, len(a)/2))
		return
	}
	fail("auxv", "no AT_PAGESZ among %d pairs", len(a)/2)
}

