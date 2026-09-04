// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package main

import (
	"fmt"
	_ "unsafe" // for linkname
)

// getAuxv reads the runtime's auxiliary vector the way an outside package
// does. golang.org/x/sys/cpu takes exactly this path in its own init.
//
//go:linkname getAuxv runtime.getAuxv
func getAuxv() []uintptr

const (
	atHWCAP     = 16
	hwcapFP     = 1 << 0
	hwcapASIMD  = 1 << 1
	hwcapNeeded = hwcapFP | hwcapASIMD
)

// checkHWCAP asserts that an arm64 host answers the AT_HWCAP question
// through the auxiliary vector.
//
// A reader that finds no answer there reads the ID_AA64ISAR* registers
// instead, and macOS answers that MRS with SIGILL. golang.org/x/sys/cpu
// does this in its own init, so a binary that merely imports it - through
// x/crypto, through go-git - dies before main on a Mac. The same value
// drives internal/cpu, so an absent one also costs the stdlib its AES,
// SHA and CRC32 assembly on every host.
func checkHWCAP() {
	auxv := getAuxv()
	for i := 0; i+1 < len(auxv); i += 2 {
		if auxv[i] != atHWCAP {
			continue
		}
		hwcap := auxv[i+1]
		if hwcap&hwcapNeeded != hwcapNeeded {
			fail("hwcap", "AT_HWCAP=%#x lacks FP|ASIMD", hwcap)
			return
		}
		ok("hwcap", fmt.Sprintf("AT_HWCAP=%#x", hwcap))
		return
	}
	fail("hwcap", "no AT_HWCAP in auxv (%d entries): a reader falls back to MRS, which macOS refuses", len(auxv))
}
