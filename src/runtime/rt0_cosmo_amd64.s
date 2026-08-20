// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

#include "textflag.h"

// The APE boot paths pass the following when jumping to the entry point on
// x86_64:
//   CL  = host OS indicator (8 = XNU/macOS, 0 = Linux, etc.)
//   RDX = path to executable (on some platforms)
//   RSP = stack pointer
//
// We need to save CL before calling the Go runtime.
//
// Only the low byte (CL) is defined by the protocol; the upper bits of RCX
// are whatever the boot path left there (undefined for an assimilated ELF
// on Linux). __hostos is a 4-byte value read with MOVL by the syscall
// dispatchers, so zero-extend CL rather than storing raw ECX.


TEXT _rt0_amd64_cosmo(SB),NOSPLIT,$-8
	// Entry point for Cosmopolitan AMD64 binaries
	// The APE boot path passes:
	//   CL  = host OS indicator (8 = XNU/macOS, 0 = Linux, etc.)
	//
	// Save host OS for runtime use
	MOVBLZX	CX, CX
	MOVL	CX, runtime·__hostos(SB)

	// Proceed to standard Go AMD64 runtime entry
	JMP	_rt0_amd64(SB)

TEXT _rt0_amd64_cosmo_lib(SB),NOSPLIT,$0
	// For shared library builds, also save the host OS info
	MOVBLZX	CX, CX
	MOVL	CX, runtime·__hostos(SB)
	JMP	_rt0_amd64_lib(SB)
