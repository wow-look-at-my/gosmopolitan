// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"fmt"
	"os"
	"unsafe" // also for go:linkname
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
// that only a Linux kernel emulates: x/sys/cpu ran it inside its package
// init on macOS and every binary that links x/crypto died of SIGILL
// before main. Linux supplies the vector on the stack; the NT stub and
// sysargs's macOS branch build one.
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

// checkProcAuxv reads the vector the way golang.org/x/sys/cpu reads it.
//
// cpu asks the runtime first, but its own package init reaches readHWCAP
// before the init that assigns getAuxvFn, so the answer is nil whatever
// the runtime holds and this file is the only route left. A Linux host
// serves it from /proc. A macOS host has no /proc, so package syscall
// answers this one path itself; without that, cpu falls through to an
// MRS of ID_AA64ISAR0_EL1 that XNU traps.
func checkProcAuxv() {
	host := cosmoHostOS()
	if host == "windows" {
		// NT has no /proc and needs none: the trap is arm64-only and no
		// arm64 payload boots here.
		ok("procauxv", "skipped: no /proc on windows")
		return
	}
	buf, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		fail("procauxv", "host %s: %v; x/sys/cpu falls back to a Linux-only CPU probe", host, err)
		return
	}
	word := int(unsafe.Sizeof(uintptr(0)))
	if len(buf) == 0 || len(buf)%(2*word) != 0 {
		fail("procauxv", "%d bytes, want whole %d-byte tag/value pairs", len(buf), 2*word)
		return
	}
	want := uint64(os.Getpagesize())
	for i := 0; i+2*word <= len(buf); i += 2 * word {
		if leUint(buf[i:], word) != atPagesz {
			continue
		}
		if got := leUint(buf[i+word:], word); got != want {
			fail("procauxv", "AT_PAGESZ %d, want the %d os.Getpagesize reports", got, want)
			return
		}
		ok("procauxv", fmt.Sprintf("pagesz=%d pairs=%d", want, len(buf)/(2*word)))
		return
	}
	fail("procauxv", "no AT_PAGESZ among %d pairs", len(buf)/(2*word))
}

// leUint reads one little-endian word, the layout the kernel writes this
// file in. Both architectures an APE boots on are little-endian.
func leUint(b []byte, word int) uint64 {
	var v uint64
	for i := word - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}
