// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

// Windows NT support assembly for cosmo/arm64.
//
// This is the arm64 twin of sys_cosmo_nt_amd64.s. Two ABI facts
// separate them. Windows/arm64 uses AAPCS64, so the first eight
// integer arguments ride x0-x7 and there is no shadow space to
// reserve. And the Thread Environment Block has its own register:
// x18, spelled R18_PLATFORM. Go's arm64 port never allocates R18
// (cmd/internal/obj/arm64: REGPR), so the loader-installed value
// survives every Go frame.
//
// Nothing here executes on a Linux or macOS host. The APE has no
// Windows/arm64 boot stub yet, so no code path reaches these
// functions at all today.

#include "go_asm.h"
#include "textflag.h"
#include "cgo/abi_arm64.h"

// Offsets into the Thread Environment Block. NT_TIB opens the TEB on
// both 64-bit Windows architectures, so StackBase and StackLimit keep
// their x64 places; TEB_error and TEB_ArbitraryPtr are the values
// upstream sys_windows_arm64.s uses.
#define TEB_StackBase 0x08
#define TEB_StackLimit 0x10
#define TEB_ArbitraryPtr 0x28
#define TEB_error 0x68

// func ntcall6(args *ntcallArgs)
//
// AAPCS64 -> win64/arm64 trampoline. Invoked through asmcgocall, which
// handles the g0 stack switch and the stack accounting, so on entry we
// are on a system stack with R0 = &ntcallArgs. Six arguments all fit
// in x0-x5, so this needs no outgoing stack space.
//
// R19 holds the args pointer across the call: AAPCS64 preserves
// x19-x28, so the callee gives it back. The g register is x28, which
// the same rule preserves, so this reads g directly instead of going
// to TLS the way the amd64 twin does.
//
// Last-error discipline: the TEB LastErrorValue slot is zeroed before
// the call (upstream asmstdcall's SetLastError(0) bracket) and
// captured into this M's mOS.ntLastError immediately after the call
// target returns. Capturing it here rather than through a second
// GetLastError call makes it atomic with the call, so no suspension
// window and no later win64 call on this thread can lose it.
TEXT runtime·ntcall6(SB),NOSPLIT|NOFRAME,$0
	SUB	$32, RSP
	MOVD	R30, 24(RSP)
	MOVD	R19, 16(RSP)
	MOVD	R0, R19

	MOVW	$0, TEB_error(R18_PLATFORM)	// SetLastError(0)

	MOVD	(ntcallArgs_fn)(R19), R12
	MOVD	(ntcallArgs_a1)(R19), R0
	MOVD	(ntcallArgs_a2)(R19), R1
	MOVD	(ntcallArgs_a3)(R19), R2
	MOVD	(ntcallArgs_a4)(R19), R3
	MOVD	(ntcallArgs_a5)(R19), R4
	MOVD	(ntcallArgs_a6)(R19), R5
	BL	(R12)

	MOVD	R0, (ntcallArgs_ret)(R19)
	MOVWU	TEB_error(R18_PLATFORM), R1
	MOVD	g_m(g), R2
	MOVW	R1, (m_mOS+mOS_ntLastError)(R2)

	MOVD	16(RSP), R19
	MOVD	24(RSP), R30
	ADD	$32, RSP
	RET

// func ntcall10(args *ntcallArgs10)
//
// Ten-argument variant of ntcall6, for win64 functions with up to ten
// parameters (CreateFileW takes 7, CreateProcessW takes 10). Arguments
// 9 and 10 go on the stack at 0(RSP) and 8(RSP); the frame stays a
// multiple of 16 so the callee sees an aligned stack.
//
//	0(RSP)   arg 9
//	8(RSP)   arg 10
//	16(RSP)  (padding)
//	24(RSP)  (padding)
//	32(RSP)  saved R19
//	40(RSP)  saved LR
//
// Last-error bracket: same discipline as ntcall6.
TEXT runtime·ntcall10(SB),NOSPLIT|NOFRAME,$0
	SUB	$48, RSP
	MOVD	R30, 40(RSP)
	MOVD	R19, 32(RSP)
	MOVD	R0, R19

	MOVW	$0, TEB_error(R18_PLATFORM)	// SetLastError(0)

	// Stack arguments first: loading them needs a scratch register,
	// and x0-x7 must already hold their final values at the call.
	MOVD	(ntcallArgs10_a9)(R19), R12
	MOVD	R12, 0(RSP)
	MOVD	(ntcallArgs10_a10)(R19), R12
	MOVD	R12, 8(RSP)

	MOVD	(ntcallArgs10_fn)(R19), R13
	MOVD	(ntcallArgs10_a1)(R19), R0
	MOVD	(ntcallArgs10_a2)(R19), R1
	MOVD	(ntcallArgs10_a3)(R19), R2
	MOVD	(ntcallArgs10_a4)(R19), R3
	MOVD	(ntcallArgs10_a5)(R19), R4
	MOVD	(ntcallArgs10_a6)(R19), R5
	MOVD	(ntcallArgs10_a7)(R19), R6
	MOVD	(ntcallArgs10_a8)(R19), R7
	BL	(R13)

	MOVD	R0, (ntcallArgs10_ret)(R19)
	MOVWU	TEB_error(R18_PLATFORM), R1
	MOVD	g_m(g), R2
	MOVW	R1, (m_mOS+mOS_ntLastError)(R2)

	MOVD	32(RSP), R19
	MOVD	40(RSP), R30
	ADD	$48, RSP
	RET

// func ntwrite1tramp(fd uintptr, p unsafe.Pointer, n int32) int32
//
// Framed bridge from the write1 asm to the Go-side ntwrite1. write1
// itself is NOSPLIT $0 and cannot make a framed call, so its NT branch
// tail-jumps here and this trampoline performs the ABI0 stack-argument
// call. arm64's write1 has no NT branch yet, so nothing reaches this.
//
// 0(RSP) holds this frame's saved LR (the arm64 prologue stores it
// there), so the outgoing ABI0 arguments start at 8(RSP): fd at 8, p at
// 16, n at 24, and the int32 result comes back at 32. The $32 frame
// covers exactly that span.
TEXT runtime·ntwrite1tramp(SB),NOSPLIT,$32-28
	MOVD	fd+0(FP), R0
	MOVD	R0, 8(RSP)
	MOVD	p+8(FP), R0
	MOVD	R0, 16(RSP)
	MOVW	n+16(FP), R0
	MOVW	R0, 24(RSP)
	BL	runtime·ntwrite1(SB)
	MOVW	32(RSP), R0
	MOVW	R0, ret+24(FP)
	RET

// func ntSetTEBg()
//
// Records g in this thread's TEB ArbitraryUserPointer. The caller runs
// on g0 (ntBootInit), so the slot holds the boot thread's g0, the same
// value tstart_cosmo_nt publishes for every created thread. Nothing
// updates the slot afterwards: save_g is a no-op without cgo, so it
// never tracks curg. ntsigtramp reads it only for a fault outside Go
// text.
TEXT runtime·ntSetTEBg(SB),NOSPLIT|NOFRAME,$0
	MOVD	g, TEB_ArbitraryPtr(R18_PLATFORM)
	RET

// tstart_cosmo_nt is the CreateThread start routine for new Ms
// (ntNewosproc, os_cosmo_nt.go). Win64 entry: R0 = mp. It pivots onto
// the Go-allocated g0 stack, publishes g0 in the TEB slot, wires
// g0<->m, and runs mstart. The tiny NT thread stack (64KiB
// reservation) is abandoned by the pivot and dies with the thread at
// ExitThread. m.procid is filled by minit.
//
// After the pivot the NT_TIB StackBase/StackLimit are rewritten to the
// WIDE window (policy in os_cosmo_nt_sig.go) so exception dispatch and
// continue validity checks accept every stack Go code can run on.
TEXT runtime·tstart_cosmo_nt(SB),NOSPLIT|NOFRAME,$0
	MOVD	R0, R20			// mp
	MOVD	m_g0(R20), R21		// g0

	// Pivot onto g0's stack.
	MOVD	(g_stack+stack_hi)(R21), R2
	AND	$~15, R2, R2
	MOVD	R2, RSP

	// TEB stack-bounds hygiene. Constants shared with the
	// ntSetTEBStackBounds callers through go_asm.h.
	MOVD	$const__NT_TEB_WIDE_BASE, R3
	MOVD	R3, TEB_StackBase(R18_PLATFORM)
	MOVD	$const__NT_TEB_WIDE_LIMIT, R3
	MOVD	R3, TEB_StackLimit(R18_PLATFORM)

	MOVD	R20, g_m(R21)
	MOVD	R21, g
	MOVD	R21, TEB_ArbitraryPtr(R18_PLATFORM)

	BL	runtime·emptyfunc(SB)	// fault if the stack check is wrong
	BL	runtime·mstart(SB)

	// mstart never returns.
	MOVD	$0, R0
	MOVD	(R0), R0

// func ntSetTEBStackBounds(hi, lo uintptr)
//
// Writes this thread's NT_TIB StackBase and StackLimit. The kernel and
// ntdll consult these during exception dispatch and unwind sanity
// checks; the fork installs a deliberately wide window (policy note in
// os_cosmo_nt_sig.go).
TEXT runtime·ntSetTEBStackBounds(SB),NOSPLIT|NOFRAME,$0-16
	MOVD	hi+0(FP), R0
	MOVD	R0, TEB_StackBase(R18_PLATFORM)
	MOVD	lo+8(FP), R0
	MOVD	R0, TEB_StackLimit(R18_PLATFORM)
	RET

// func ntGetTEBStackBounds() (hi, lo uintptr)
TEXT runtime·ntGetTEBStackBounds(SB),NOSPLIT|NOFRAME,$0-16
	MOVD	TEB_StackBase(R18_PLATFORM), R0
	MOVD	R0, hi+0(FP)
	MOVD	TEB_StackLimit(R18_PLATFORM), R0
	MOVD	R0, lo+8(FP)
	RET

// ntsigtramp is the common body of the three exception-callback
// thunks. Entry from the NT exception dispatcher: R0 =
// EXCEPTION_POINTERS*, R1 = callback kind (loaded by the registration
// thunks below). Modeled on upstream sys_windows_arm64.s sigtramp: it
// saves the AAPCS64 callee-saved set that Go's ABI0 treats as scratch,
// establishes g, and calls runtime.ntSigtrampGo(ep, kind). The int32
// verdict goes back to the dispatcher in R0.
//
// g is the faulting thread's x28 from the saved CONTEXT when the
// faulting PC lies in Go text: Go code keeps g in x28 at all times, and
// the TEB ArbitraryUserPointer slot never tracks curg (it holds the
// thread's g0 for the thread's whole life). For a PC outside Go text
// x28 is whatever the foreign code left there, so g comes from the TEB
// slot instead, which is 0 on a thread that never ran Go code; the
// nosplit ntSigtrampGo checks that.
//
// Frame layout: 0(RSP) saved LR, 8/16(RSP) the ABI0 arguments to
// ntSigtrampGo, 24(RSP) its result, 32..112 R19-R28, 112..176 F8-F15.
//
// Runs on the faulting thread's current stack (goroutine stacks
// included - stackSystem reserves the dispatch headroom); the Go side
// hops to g0 via systemstack.
TEXT ntsigtramp<>(SB),NOSPLIT,$176
	SAVE_R19_TO_R28(8*4)
	SAVE_F8_TO_F15(8*14)

	MOVD	R0, R19		// ep
	MOVD	R1, R20		// kind

	MOVD	(ntExceptionPointers_context)(R19), R2
	MOVD	(ntContextARM64_pc)(R2), R3
	MOVD	$runtime·firstmoduledata(SB), R4
	MOVD	(moduledata_text)(R4), R5
	MOVD	(moduledata_etext)(R4), R6
	CMP	R5, R3
	BLO	sigtramp_foreign	// pc < text
	CMP	R6, R3
	BHI	sigtramp_foreign	// etext < pc
	MOVD	(ntContextARM64_x+28*8)(R2), g
	B	sigtramp_haveg
sigtramp_foreign:
	MOVD	TEB_ArbitraryPtr(R18_PLATFORM), g
sigtramp_haveg:

	MOVD	R19, 8(RSP)
	MOVW	R20, 16(RSP)
	BL	runtime·ntSigtrampGo(SB)
	MOVW	24(RSP), R0	// verdict (int32)

	RESTORE_R19_TO_R28(8*4)
	RESTORE_F8_TO_F15(8*14)
	RET

TEXT runtime·ntExceptionTramp(SB),NOSPLIT|NOFRAME,$0
	MOVD	$const_ntCallbackVEH, R1
	B	ntsigtramp<>(SB)

TEXT runtime·ntFirstVCHTramp(SB),NOSPLIT|NOFRAME,$0
	MOVD	$const_ntCallbackFirstVCH, R1
	B	ntsigtramp<>(SB)

TEXT runtime·ntLastVCHTramp(SB),NOSPLIT|NOFRAME,$0
	MOVD	$const_ntCallbackLastVCH, R1
	B	ntsigtramp<>(SB)

// func ntExitEncoded(sig uint32)
//
// ExitProcess(0xC0DE0000|sig&0x7F): the fork-private signal-death exit
// status. Never returns.
TEXT runtime·ntExitEncoded(SB),NOSPLIT,$16-4
	MOVWU	sig+0(FP), R0
	AND	$0x7F, R0, R0
	MOVD	$0xC0DE0000, R1
	ORR	R1, R0, R0
	MOVD	runtime·ntExitProcessFn(SB), R12
	BL	(R12)
	MOVD	$0, R0
	MOVD	(R0), R0	// unreachable

// func ntCtrlTramp()
//
// SetConsoleCtrlHandler callback. Win64 entry: R0 = dwCtrlType.
// Windows runs this on an INJECTED foreign thread - no g - so it must
// stay free of Go and of ntcall (asmcgocall needs a g): classify the
// event, OR the signal bit into ntCtrlMask, wake the relay M through
// its dedicated event, and either report "handled" (the keyboard
// chords CTRL_C -> SIGINT and CTRL_BREAK -> SIGQUIT, where the relay
// owns the outcome including default death) or block forever (the
// process-lifetime events CLOSE -> SIGHUP and LOGOFF/SHUTDOWN ->
// SIGTERM, because Windows kills the process the moment such a handler
// returns; blocking gives the Go handlers the OS grace window). Only
// volatile registers are used, so the win64 callee-saved set is
// preserved by construction.
TEXT runtime·ntCtrlTramp(SB),NOSPLIT,$32-0
	MOVD	R0, R1
	CMP	$0, R1
	BEQ	ctrl_int	// CTRL_C_EVENT(0) -> SIGINT
	CMP	$1, R1
	BEQ	ctrl_quit	// CTRL_BREAK_EVENT(1) -> SIGQUIT
	CMP	$2, R1
	BEQ	ctrl_hup	// CTRL_CLOSE_EVENT(2) -> SIGHUP, block
	CMP	$5, R1
	BEQ	ctrl_term	// CTRL_LOGOFF_EVENT(5) -> SIGTERM, block
	CMP	$6, R1
	BEQ	ctrl_term	// CTRL_SHUTDOWN_EVENT(6) -> SIGTERM, block
	MOVD	$0, R0		// not ours: FALSE -> next handler
	RET
ctrl_int:
	MOVD	$4, R2		// 1<<_SIGINT(2)
	BL	ntCtrlOr<>(SB)
	B	ctrl_wake_ret
ctrl_quit:
	MOVD	$8, R2		// 1<<_SIGQUIT(3)
	BL	ntCtrlOr<>(SB)
ctrl_wake_ret:
	MOVD	runtime·ntCtrlEvent(SB), R0
	MOVD	runtime·ntSetEventFn(SB), R12
	BL	(R12)
	MOVD	$1, R0		// TRUE: handled
	RET
ctrl_hup:
	MOVD	$2, R2		// 1<<_SIGHUP(1)
	BL	ntCtrlOr<>(SB)
	B	ctrl_wake_block
ctrl_term:
	MOVD	$0x8000, R2	// 1<<_SIGTERM(15)
	BL	ntCtrlOr<>(SB)
ctrl_wake_block:
	MOVD	runtime·ntCtrlEvent(SB), R0
	MOVD	runtime·ntSetEventFn(SB), R12
	BL	(R12)
ctrl_block:
	MOVD	$0xFFFFFFFF, R0	// INFINITE
	MOVD	runtime·ntSleepFn(SB), R12
	BL	(R12)
	B	ctrl_block

// ntCtrlOr atomically ORs R2 into runtime.ntCtrlMask. Clobbers R3-R5
// only, so ntCtrlTramp's classification result survives the call.
TEXT ntCtrlOr<>(SB),NOSPLIT|NOFRAME,$0
	MOVD	$runtime·ntCtrlMask(SB), R3
ntCtrlOr_retry:
	LDAXRW	(R3), R4
	ORR	R2, R4, R4
	STLXRW	R4, (R3), R5
	CBNZ	R5, ntCtrlOr_retry
	RET

// func ntSignalTramp(fn, sig uintptr, info, ctx unsafe.Pointer, sp uintptr)
//
// Kernel-mimic signal delivery (ntDeliverSelfSignal): switch to sp
// (the top of this M's gsignal stack) and call the recorded handler -
// the runtime's C-ABI sigtramp - with the Linux handler signature
// (R0=sig, R1=info, R2=ctx). sigtramp preserves the AAPCS64
// callee-saved set, so the caller's SP survives in R19. The handler
// returns normally (there is no rt_sigreturn on NT) and execution
// continues in the Go caller.
TEXT runtime·ntSignalTramp(SB),NOSPLIT,$32-40
	MOVD	fn+0(FP), R9
	MOVD	sig+8(FP), R0
	MOVD	info+16(FP), R1
	MOVD	ctx+24(FP), R2
	MOVD	sp+32(FP), R3
	MOVD	RSP, R19
	AND	$~15, R3, R3
	MOVD	R3, RSP
	BL	(R9)
	MOVD	R19, RSP
	RET
