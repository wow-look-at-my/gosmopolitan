// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

//
// System calls for AMD64, Cosmopolitan
// Uses Linux syscall conventions
//

// Host OS indicators (must match runtime values)
#define HOSTWINDOWS 2

// Helper macro: check if we're on Windows NT and jump to label if so.
// These two entry points issue raw SYSCALLs and have no way to report
// an error, so on NT they die with loud, distinct crash pokes (the
// fault address names the failure; registry in runtime/os_cosmo_nt.go)
// instead of executing undefined behavior.
// Clobbers R11
#define CHECK_WINDOWS(label) \
	MOVL	runtime·__hostos(SB), R11; \
	CMPL	R11, $HOSTWINDOWS; \
	JEQ	label

// func rawVforkSyscall(trap, a1, a2, a3 uintptr) (r1, err uintptr)
TEXT ·rawVforkSyscall(SB),NOSPLIT|NOFRAME,$0-48
	CHECK_WINDOWS(vfork_nt_crash)
	MOVQ	a1+8(FP), DI
	MOVQ	a2+16(FP), SI
	MOVQ	a3+24(FP), DX
	MOVQ	$0, R10
	MOVQ	$0, R8
	MOVQ	$0, R9
	MOVQ	trap+0(FP), AX	// syscall entry
	POPQ	R12 // preserve return address
	SYSCALL
	PUSHQ	R12
	CMPQ	AX, $0xfffffffffffff001
	JLS	ok2
	MOVQ	$-1, r1+32(FP)
	NEGQ	AX
	MOVQ	AX, err+40(FP)
	RET
ok2:
	MOVQ	AX, r1+32(FP)
	MOVQ	$0, err+40(FP)
	RET
vfork_nt_crash:
	MOVL	$0xf8, 0xf8	// crash: rawVforkSyscall reached on NT
	RET

// func rawSyscallNoErrorAsm(trap, a1, a2, a3 uintptr) (r1, r2 uintptr)
//
// The Go wrapper rawSyscallNoError (syscall_cosmo.go) routes NT hosts
// through the emulation table before reaching this entry, so the
// CHECK_WINDOWS poke below is belt-and-suspenders: it can only fire if
// the wrapper is bypassed.
TEXT ·rawSyscallNoErrorAsm(SB),NOSPLIT,$0-48
	CHECK_WINDOWS(rawnoerr_nt_crash)
	MOVQ	a1+8(FP), DI
	MOVQ	a2+16(FP), SI
	MOVQ	a3+24(FP), DX
	MOVQ	trap+0(FP), AX	// syscall entry
	SYSCALL
	MOVQ	AX, r1+32(FP)
	MOVQ	DX, r2+40(FP)
	RET
rawnoerr_nt_crash:
	MOVL	$0xf7, 0xf7	// crash: rawSyscallNoError reached on NT
	RET
