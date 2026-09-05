// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package main

import "fmt"

// getAuxv is declared once for the whole package, in auxv_cosmo.go.

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
// instead, and macOS answers that MRS with SIGILL. That is what killed
// any binary importing golang.org/x/sys/cpu - through x/crypto, through
// go-git - before main on a Mac. This value is what internal/cpu reads,
// so an absent one also costs the stdlib its AES, SHA and CRC32
// assembly; x/sys/cpu reads /proc/self/auxv instead, which the procauxv
// check covers.
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
