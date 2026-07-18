// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Windows NT support assembly for cosmo/amd64 (wave 1).
//
// Everything here is inert until the NT boot stub (_rt0_cosmo_nt,
// rt0_cosmo_nt_amd64.s) stops exiting early and sets __hostos to
// _HOSTWINDOWS: no code path reaches these functions on Linux or
// macOS hosts.

#include "go_asm.h"
#include "go_tls.h"
#include "textflag.h"

// func ntcall6(args *ntcallArgs)
//
// SysV -> win64 trampoline. Invoked through asmcgocall (which handles
// the g0 stack switch and stack accounting), so on entry we are on a
// system stack with the C ABI in effect: DI = &ntcallArgs, and the
// SysV callee-saved set (BX, BP, R12-R15) must be preserved. Win64
// callees additionally preserve DI and SI, so DI still points at the
// args struct after the call.
//
// Entry SP is 8 mod 16 (return address pushed by CALL). SUBQ $56
// realigns to 16 and provides the 32-byte shadow space plus the two
// stack argument slots win64 needs for a 6-argument call:
//
//	0(SP)..31(SP)  shadow space
//	32(SP)         arg 5
//	40(SP)         arg 6
TEXT runtime·ntcall6(SB),NOSPLIT|NOFRAME,$0
	SUBQ	$56, SP
	MOVQ	(ntcallArgs_fn)(DI), AX
	MOVQ	(ntcallArgs_a1)(DI), CX
	MOVQ	(ntcallArgs_a2)(DI), DX
	MOVQ	(ntcallArgs_a3)(DI), R8
	MOVQ	(ntcallArgs_a4)(DI), R9
	MOVQ	(ntcallArgs_a5)(DI), R10
	MOVQ	R10, 32(SP)
	MOVQ	(ntcallArgs_a6)(DI), R10
	MOVQ	R10, 40(SP)
	CALL	AX
	MOVQ	AX, (ntcallArgs_ret)(DI)
	ADDQ	$56, SP
	RET

// func ntcall10(args *ntcallArgs10)
//
// Ten-argument variant of ntcall6 (same calling discipline) for win64
// functions with up to ten parameters (CreateFileW takes 7,
// CreateProcessW takes 10). SUBQ $88 keeps 16-alignment and adds the
// six stack argument slots:
//
//	0(SP)..31(SP)  shadow space
//	32(SP)         arg 5
//	40(SP)         arg 6
//	48(SP)         arg 7
//	56(SP)         arg 8
//	64(SP)         arg 9
//	72(SP)         arg 10
//	80(SP)         (padding for 16-alignment)
TEXT runtime·ntcall10(SB),NOSPLIT|NOFRAME,$0
	SUBQ	$88, SP
	MOVQ	(ntcallArgs10_fn)(DI), AX
	MOVQ	(ntcallArgs10_a1)(DI), CX
	MOVQ	(ntcallArgs10_a2)(DI), DX
	MOVQ	(ntcallArgs10_a3)(DI), R8
	MOVQ	(ntcallArgs10_a4)(DI), R9
	MOVQ	(ntcallArgs10_a5)(DI), R10
	MOVQ	R10, 32(SP)
	MOVQ	(ntcallArgs10_a6)(DI), R10
	MOVQ	R10, 40(SP)
	MOVQ	(ntcallArgs10_a7)(DI), R10
	MOVQ	R10, 48(SP)
	MOVQ	(ntcallArgs10_a8)(DI), R10
	MOVQ	R10, 56(SP)
	MOVQ	(ntcallArgs10_a9)(DI), R10
	MOVQ	R10, 64(SP)
	MOVQ	(ntcallArgs10_a10)(DI), R10
	MOVQ	R10, 72(SP)
	CALL	AX
	MOVQ	AX, (ntcallArgs10_ret)(DI)
	ADDQ	$88, SP
	RET

// func ntwrite1tramp(fd uintptr, p unsafe.Pointer, n int32) int32
//
// Framed bridge from the write1 asm (sys_cosmo_amd64.s) to the Go-side
// ntwrite1: write1 itself is NOSPLIT $0 and cannot make a framed call,
// so its NT branch tail-jumps here (same signature, FP slots carry
// over) and this trampoline performs the ABI0 stack-argument call.
TEXT runtime·ntwrite1tramp(SB),NOSPLIT,$40-28
	MOVQ	fd+0(FP), AX
	MOVQ	AX, 0(SP)
	MOVQ	p+8(FP), AX
	MOVQ	AX, 8(SP)
	MOVL	n+16(FP), AX
	MOVL	AX, 16(SP)
	CALL	runtime·ntwrite1(SB)
	MOVL	24(SP), AX
	MOVL	AX, ret+24(FP)
	RET

// tstart_cosmo_nt is the CreateThread start routine for new Ms
// (ntNewosproc, os_cosmo_nt.go). Win64 entry: CX = mp. It reproduces
// the clone-child setup from sys_cosmo_amd64.s minus the syscall
// parts: pivot onto the Go-allocated g0 stack, install g0 in TLS
// (gs:0x28 - on NT that is the TEB ArbitraryUserPointer slot, so a
// plain store, no settls call), wire g0<->m, and run mstart. The tiny
// NT thread stack (64KiB reservation) is abandoned by the pivot and
// dies with the thread at ExitThread; the TEB StackBase/StackLimit are
// deliberately left stale (cosmo precedent - VEH-only signal handling
// makes that safe, revisit in the signals wave). m.procid stays 0 in
// wave 1 (no GetCurrentThreadId in the resolve set; signal sends are
// dropped on NT anyway).
TEXT runtime·tstart_cosmo_nt(SB),NOSPLIT|NOFRAME,$0
	MOVQ	CX, R13			// mp (same register the clone child uses)
	MOVQ	m_g0(R13), R9		// g0

	// Pivot onto g0's stack.
	MOVQ	(g_stack+stack_hi)(R9), SI
	ANDQ	$~15, SI
	MOVQ	SI, SP

	// Set up new stack (the clone-child sequence).
	get_tls(CX)
	MOVQ	R13, g_m(R9)
	MOVQ	R9, g(CX)		// gs:0x28 = g0
	MOVQ	R9, R14			// set g register
	CALL	runtime·stackcheck(SB)

	CALL	runtime·mstart(SB)
	INT	$3	// mstart never returns
