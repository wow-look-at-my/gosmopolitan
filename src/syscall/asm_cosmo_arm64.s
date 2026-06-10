// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

//
// System calls for ARM64, Cosmopolitan
// Uses Linux syscall conventions
//

// func rawVforkSyscall(trap, a1, a2, a3 uintptr) (r1, err uintptr)
TEXT ·rawVforkSyscall(SB),NOSPLIT,$0-48
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

// func rawSyscallNoError(trap, a1, a2, a3 uintptr) (r1, r2 uintptr)
TEXT ·rawSyscallNoError(SB),NOSPLIT,$0-48
	MOVD	trap+0(FP), R8	// syscall number
	MOVD	a1+8(FP), R0
	MOVD	a2+16(FP), R1
	MOVD	a3+24(FP), R2
	SVC
	MOVD	R0, r1+32(FP)
	MOVD	R1, r2+40(FP)
	RET
