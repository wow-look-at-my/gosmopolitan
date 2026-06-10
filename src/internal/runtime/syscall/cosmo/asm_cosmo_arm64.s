// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

#include "textflag.h"

// Host OS indicators (must match runtime values)
#define HOSTXNU 8

// Linux ARM64 syscall numbers
#define SYS_exit		93
#define SYS_read		63
#define SYS_write		64
#define SYS_openat		56
#define SYS_close		57
#define SYS_mmap		222
#define SYS_munmap		215
#define SYS_mprotect		226
#define SYS_nanosleep		101
#define SYS_clock_gettime	113
#define SYS_sigaction		134
#define SYS_sigaltstack		132
#define SYS_pselect6		72

// Helper macro: check if we're on macOS and jump to label if so
// Clobbers R9
#define CHECK_DARWIN(label) \
	MOVW	runtime·__hostos(SB), R9; \
	CMPW	$HOSTXNU, R9; \
	BEQ	label

// func Syscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr)
//
// Cosmopolitan Libc uses Linux syscall conventions on all platforms.
// For arm64, the syscall number goes in R8, arguments in R0-R5.
// On Darwin ARM64, raw syscalls crash with SIGSYS - we must route
// through syslib functions instead.
TEXT ·Syscall6(SB),NOSPLIT,$0-80
	CHECK_DARWIN(syscall6_darwin)

	// Linux path: direct syscall (unchanged)
	MOVD	num+0(FP), R8	// syscall number
	MOVD	a1+8(FP), R0
	MOVD	a2+16(FP), R1
	MOVD	a3+24(FP), R2
	MOVD	a4+32(FP), R3
	MOVD	a5+40(FP), R4
	MOVD	a6+48(FP), R5
	SVC
	CMN	$4095, R0
	BCC	ok
	MOVD	$-1, R4
	MOVD	R4, r1+56(FP)
	MOVD	ZR, r2+64(FP)
	NEG	R0, R0
	MOVD	R0, errno+72(FP)
	RET
ok:
	MOVD	R0, r1+56(FP)
	MOVD	R1, r2+64(FP)
	MOVD	ZR, errno+72(FP)
	RET

// Darwin path: dispatch to syslib functions based on syscall number
syscall6_darwin:
	MOVD	num+0(FP), R8		// syscall number
	MOVD	runtime·__syslib(SB), R10
	CBZ	R10, darwin_enosys	// No syslib - return ENOSYS

	// Load arguments for C call (used by all paths)
	MOVD	a1+8(FP), R0
	MOVD	a2+16(FP), R1
	MOVD	a3+24(FP), R2
	MOVD	a4+32(FP), R3
	MOVD	a5+40(FP), R4
	MOVD	a6+48(FP), R5

	// Dispatch based on syscall number
	CMPW	$SYS_write, R8
	BEQ	darwin_write
	CMPW	$SYS_read, R8
	BEQ	darwin_read
	CMPW	$SYS_close, R8
	BEQ	darwin_close
	CMPW	$SYS_openat, R8
	BEQ	darwin_openat
	CMPW	$SYS_mmap, R8
	BEQ	darwin_mmap
	CMPW	$SYS_munmap, R8
	BEQ	darwin_munmap
	CMPW	$SYS_mprotect, R8
	BEQ	darwin_mprotect
	CMPW	$SYS_exit, R8
	BEQ	darwin_exit
	CMPW	$SYS_nanosleep, R8
	BEQ	darwin_nanosleep
	CMPW	$SYS_clock_gettime, R8
	BEQ	darwin_clock_gettime
	CMPW	$SYS_sigaction, R8
	BEQ	darwin_sigaction
	CMPW	$SYS_sigaltstack, R8
	BEQ	darwin_sigaltstack
	CMPW	$SYS_pselect6, R8
	BEQ	darwin_pselect

	// Unknown syscall - return ENOSYS
darwin_enosys:
	MOVD	$-1, R0
	MOVD	R0, r1+56(FP)
	MOVD	ZR, r2+64(FP)
	MOVD	$38, R0			// ENOSYS
	MOVD	R0, errno+72(FP)
	RET

// Syslib function calls - offsets match runtime/sys_cosmo_arm64.s
darwin_write:
	MOVD	256(R10), R12		// syslib.write
	B	darwin_call
darwin_read:
	MOVD	264(R10), R12		// syslib.read
	B	darwin_call
darwin_close:
	MOVD	232(R10), R12		// syslib.close
	B	darwin_call
darwin_openat:
	MOVD	248(R10), R12		// syslib.openat
	B	darwin_call
darwin_mmap:
	// mmap needs flag translation: Linux MAP_ANONYMOUS (0x20) -> macOS MAP_ANON (0x1000)
	// R3 = flags argument
	AND	$0x20, R3, R6		// Extract MAP_ANONYMOUS bit
	CMP	$0, R6
	BEQ	darwin_mmap_call
	AND	$~0x20, R3, R3		// Clear Linux MAP_ANONYMOUS
	ORR	$0x1000, R3, R3		// Set macOS MAP_ANON
darwin_mmap_call:
	MOVD	40(R10), R12		// syslib.mmap
	B	darwin_call
darwin_munmap:
	MOVD	240(R10), R12		// syslib.munmap
	B	darwin_call
darwin_mprotect:
	MOVD	288(R10), R12		// syslib.mprotect
	B	darwin_call
darwin_exit:
	MOVD	224(R10), R12		// syslib.exit
	B	darwin_call
darwin_nanosleep:
	MOVD	32(R10), R12		// syslib.nanosleep
	B	darwin_call
darwin_clock_gettime:
	MOVD	24(R10), R12		// syslib.clock_gettime
	B	darwin_call
darwin_sigaction:
	MOVD	272(R10), R12		// syslib.sigaction
	B	darwin_call
darwin_sigaltstack:
	MOVD	296(R10), R12		// syslib.sigaltstack
	B	darwin_call
darwin_pselect:
	MOVD	280(R10), R12		// syslib.pselect
	B	darwin_call

darwin_call:
	CBZ	R12, darwin_enosys	// Function not in syslib
	SUB	$16, RSP		// Align stack for C ABI
	BL	(R12)
	ADD	$16, RSP

	// Handle return value: negative = -errno, non-negative = success
	CMN	$4095, R0
	BCC	darwin_success

	// Error case: R0 contains -errno
	NEG	R0, R0			// Make errno positive
	MOVD	$-1, R1
	MOVD	R1, r1+56(FP)
	MOVD	ZR, r2+64(FP)
	MOVD	R0, errno+72(FP)
	RET

darwin_success:
	MOVD	R0, r1+56(FP)
	MOVD	R1, r2+64(FP)
	MOVD	ZR, errno+72(FP)
	RET
