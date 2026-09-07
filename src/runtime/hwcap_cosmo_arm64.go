// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

// The HWCAP bits an arm64 program reads out of the auxiliary vector to
// learn what the CPU can do. Linux publishes them. macOS publishes the
// same answers through sysctl, and the APE answers in Linux vocabulary,
// so darwinHWCAP translates.
//
// hwcap_CPUID is the one bit here that is never SET, only cleared. It
// advertises that the kernel emulates the ID_AA64ISAR* registers, and a
// reader that believes it executes MRS, which macOS answers with SIGILL.
const (
	hwcap_FP      = 1 << 0
	hwcap_ASIMD   = 1 << 1
	hwcap_AES     = 1 << 3
	hwcap_PMULL   = 1 << 4
	hwcap_SHA1    = 1 << 5
	hwcap_SHA2    = 1 << 6
	hwcap_CRC32   = 1 << 7
	hwcap_ATOMICS = 1 << 8
	hwcap_CPUID   = 1 << 11
	hwcap_SHA3    = 1 << 17
	hwcap_SHA512  = 1 << 21
	hwcap_DIT     = 1 << 24
)

// The keys Apple documents for these features. macOS 12 moved them under
// hw.optional.arm; the older spellings still answer, and internal/cpu's
// own darwin port reads the same set.
var (
	sysctlArmv81Atomics = []byte("hw.optional.armv8_1_atomics\x00")
	sysctlArmv8Crc32    = []byte("hw.optional.armv8_crc32\x00")
	sysctlArmv82Sha512  = []byte("hw.optional.armv8_2_sha512\x00")
	sysctlArmv82Sha3    = []byte("hw.optional.armv8_2_sha3\x00")
	sysctlArmFeatDit    = []byte("hw.optional.arm.FEAT_DIT\x00")
)

// The auxv fixAuxv hands out. It holds whatever the host passed, plus
// the AT_HWCAP pair, so a reader that walks it sees both.
var darwinAuxvBuf [64]uintptr

// fixAuxv makes a macOS host's AT_HWCAP safe to believe, so internal/cpu
// enables the arm64 AES/SHA/CRC32 assembly there. The APE loader
// usually passes a pair already, and it sets hwcap_CPUID
// - a claim that the kernel emulates the ID_AA64ISAR* registers, which
// Linux does and XNU does not. internal/cpu answers that claim with an
// MRS for the MIDR. So the loader's pair is taken over with that one
// bit cleared, and only a host passing no pair gets the sysctl value.
//
// This does not save golang.org/x/sys/cpu from its own SIGILL: it
// reads /proc/self/auxv, never this vector, so syscall's
// procauxv_cosmo.go is the half that keeps it off the MRS, over
// whatever this settled on.
func fixAuxv() {
	if !isdarwin() {
		return
	}
	for i := 0; i+1 < len(auxv); i += 2 {
		if auxv[i] != _AT_HWCAP {
			continue
		}
		hwcap := auxv[i+1] &^ hwcap_CPUID
		if hwcap == auxv[i+1] {
			archauxv(_AT_HWCAP, hwcap)
			return
		}
		// The vector sits on the boot stack, so edit a copy.
		n := copy(darwinAuxvBuf[:len(darwinAuxvBuf)-2], auxv)
		darwinAuxvBuf[i+1] = hwcap
		darwinAuxvBuf[n] = _AT_NULL
		darwinAuxvBuf[n+1] = 0
		auxv = darwinAuxvBuf[:n:n]
		archauxv(_AT_HWCAP, hwcap)
		return
	}
	n := copy(darwinAuxvBuf[:len(darwinAuxvBuf)-4], auxv)
	hwcap := darwinHWCAP()
	darwinAuxvBuf[n] = _AT_HWCAP
	darwinAuxvBuf[n+1] = hwcap
	darwinAuxvBuf[n+2] = _AT_NULL
	darwinAuxvBuf[n+3] = 0
	auxv = darwinAuxvBuf[: n+2 : n+2]
	archauxv(_AT_HWCAP, hwcap)
}

// darwinHWCAP reports the host CPU's features as an AT_HWCAP value.
func darwinHWCAP() uintptr {
	// Every Apple Silicon part has these, and macOS 11 publishes no
	// sysctl to ask. internal/cpu's darwin port assumes them too.
	hwcap := uintptr(hwcap_FP | hwcap_ASIMD | hwcap_AES | hwcap_PMULL | hwcap_SHA1 | hwcap_SHA2)
	if cosmoDarwinSysctlEnabled(&sysctlArmv81Atomics[0]) {
		hwcap |= hwcap_ATOMICS
	}
	if cosmoDarwinSysctlEnabled(&sysctlArmv8Crc32[0]) {
		hwcap |= hwcap_CRC32
	}
	if cosmoDarwinSysctlEnabled(&sysctlArmv82Sha512[0]) {
		hwcap |= hwcap_SHA512
	}
	if cosmoDarwinSysctlEnabled(&sysctlArmv82Sha3[0]) {
		hwcap |= hwcap_SHA3
	}
	if cosmoDarwinSysctlEnabled(&sysctlArmFeatDit[0]) {
		hwcap |= hwcap_DIT
	}
	return hwcap
}
