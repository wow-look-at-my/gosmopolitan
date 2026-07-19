// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

//
// System calls for ARM64, Cosmopolitan
// Uses Linux syscall conventions on Linux hosts (direct SVC). On macOS
// raw SVC is forbidden (SIGSYS), so both entry points route through the
// APE loader's Syslib / the generic cosmo dispatcher instead.
//

// Host OS indicator (must match runtime values)
#define HOSTXNU 8

// Linux ARM64 syscall numbers
#define SYS_clone	220

// Helper macro: check if we're on macOS and jump to label if so
// Clobbers R9
#define CHECK_DARWIN(label) \
	MOVW	runtime·__hostos(SB), R9; \
	CMPW	$HOSTXNU, R9; \
	BEQ	label

// func rawVforkSyscall(trap, a1, a2, a3 uintptr) (r1, err uintptr)
TEXT ·rawVforkSyscall(SB),NOSPLIT,$0-48
	CHECK_DARWIN(vfork_darwin)
	// Linux path: direct syscall
	MOVD	trap+0(FP), R8	// syscall number
	MOVD	a1+8(FP), R0
	MOVD	a2+16(FP), R1
	MOVD	a3+24(FP), R2
	SVC
	CMN	$4095, R0
	BLS	ok
	MOVD	$-1, R1
	MOVD	R1, r1+32(FP)
	NEG	R0, R0
	MOVD	R0, err+40(FP)
	RET
ok:
	MOVD	R0, r1+32(FP)
	MOVD	ZR, err+40(FP)
	RET

vfork_darwin:
	// The only caller is forkExec, which passes SYS_CLONE with SIGCHLD
	// (plain fork semantics). Map that to the Syslib's fork; anything
	// else is unsupported on macOS and fails with ENOSYS. This path
	// must stay pure assembly: the child returns through here and must
	// not grow the stack.
	MOVD	trap+0(FP), R8
	CMPW	$SYS_clone, R8
	BNE	vfork_darwin_enosys
	MOVD	runtime·__syslib(SB), R9
	CBZ	R9, vfork_darwin_enosys
	MOVD	8(R9), R12		// syslib.fork (Syslib v1+)
	CBZ	R12, vfork_darwin_enosys
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	// Apple fork matches Linux clone(SIGCHLD) semantics: child pid in
	// the parent, 0 in the child, -errno on failure - except the errno
	// uses Apple numbering (e.g. EAGAIN is 35, not 11), so translate.
	CMN	$4095, R0
	BCC	vfork_darwin_ok
	NEG	R0, R0
	BL	runtime·cosmo_xlat_errno_r0(SB)
	MOVD	$-1, R1
	MOVD	R1, r1+32(FP)
	MOVD	R0, err+40(FP)
	RET
vfork_darwin_ok:
	MOVD	R0, r1+32(FP)
	MOVD	ZR, err+40(FP)
	RET
vfork_darwin_enosys:
	MOVD	$-1, R0
	MOVD	R0, r1+32(FP)
	MOVD	$38, R0			// Linux ENOSYS
	MOVD	R0, err+40(FP)
	RET

// func rawSyscallNoErrorAsm(trap, a1, a2, a3 uintptr) (r1, r2 uintptr)
//
// The Go wrapper rawSyscallNoError (syscall_cosmo.go) handles the NT
// route (which cannot occur on arm64 - iswindows is constant false)
// and otherwise lands here.
TEXT ·rawSyscallNoErrorAsm(SB),NOSPLIT,$0-48
	CHECK_DARWIN(rawnoerr_darwin)
	// Linux path: direct syscall
	MOVD	trap+0(FP), R8	// syscall number
	MOVD	a1+8(FP), R0
	MOVD	a2+16(FP), R1
	MOVD	a3+24(FP), R2
	SVC
	MOVD	R0, r1+32(FP)
	MOVD	R1, r2+40(FP)
	RET
rawnoerr_darwin:
	// Raw SVC would SIGSYS on macOS (this took out os.Getpid, Umask and
	// friends). Tail-jump to the Go shim (identical signature), which
	// routes through the generic cosmo dispatcher and its dlsym-backed
	// darwin emulation.
	//
	// NOTE: this tail JMP is only correct because rawSyscallNoError is
	// a LEAF (no BL anywhere), so the assembler gives it no stack
	// frame. If a BL is ever added to this function, convert the JMP
	// into a framed CALL like Syscall6's darwin slow path.
	JMP	·rawSyscallNoErrorDarwin(SB)
