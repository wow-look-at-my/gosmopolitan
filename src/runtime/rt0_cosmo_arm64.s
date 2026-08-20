// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

#include "textflag.h"

// The APE loader (ape-m1.c) passes the following when jumping to the entry point:
//   X0  = stack pointer (not used, SP is already set)
//   X2  = path to executable
//   X3  = host OS indicator (8 = XNU/macOS, 0 = Linux, etc.)
//   X15 = Syslib pointer (Apple APIs for macOS)
//   X16 = entry point (used for the jump, not relevant here)
//
// We need to save X3 and X15 before calling the Go runtime.



TEXT _rt0_arm64_cosmo(SB),NOSPLIT|NOFRAME,$0
	// Entry point for Cosmopolitan ARM64 binaries
	// The APE loader (ape-m1.c) passes:
	//   X0  = stack pointer (not used, SP is already set)
	//   X2  = path to executable
	//   X3  = host OS indicator (8 = XNU/macOS, 0 = Linux, etc.)
	//   X15 = Syslib pointer (Apple APIs for macOS)
	//
	// Detect the APE loader (macOS) handoff: X3 must be 8 (XNU) and X15
	// must point at a Syslib structure with the right magic. Anything
	// else is a plain ELF boot (e.g. Linux ARM64 after the bootstrap
	// script self-assimilates the binary), where these registers are
	// undefined and raw SVC syscalls are used instead of Syslib.
	CMP	$8, R3
	BNE	boot_linux
	CBZ	R15, boot_linux
	MOVW	(R15), R9
	MOVW	$0x62696c73, R10	// Syslib magic "slib"
	CMP	R9, R10
	BNE	boot_linux

	// XNU via APE loader: save host OS and Syslib pointer for runtime use
	MOVW	R3, runtime·__hostos(SB)
	MOVD	R15, runtime·__syslib(SB)
	B	cosmo_init_ok

boot_linux:
	MOVW	ZR, runtime·__hostos(SB)
	MOVD	ZR, runtime·__syslib(SB)

cosmo_init_ok:
	// Ensure stack is 16-byte aligned (required by ARM64 ABI)
	MOVD	RSP, R0
	AND	$~15, R0, RSP

	// Proceed to standard Go ARM64 runtime entry
	JMP	_rt0_arm64(SB)

TEXT _rt0_arm64_cosmo_lib(SB),NOSPLIT,$0
	// For shared library builds, also save the Syslib info
	MOVW	R3, runtime·__hostos(SB)
	MOVD	R15, runtime·__syslib(SB)
	JMP	_rt0_arm64_lib(SB)
