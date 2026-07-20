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
//
// Last-error discipline (chunk D2): the TEB LastErrorValue slot
// (TEB+0x68, TEB linear address at gs:0x30) is zeroed before the call
// (upstream asmstdcall's SetLastError(0) bracket) and captured into
// this M's mOS.ntLastError immediately after the call target returns -
// atomically with the call, so the value can never be lost to a
// suspension window or clobbered by a later win64 call on this thread
// (the pre-D2 two-call GetLastError fetch was only correct while
// SuspendThread preemption did not exist). The TLS g is valid in every
// ntcall6/ntcall10 context: boot runs after rt0_go's TLS setup, and
// the only foreign-thread entry points (VEH thunk, console-ctrl
// handler) never reach these trampolines.
TEXT runtime·ntcall6(SB),NOSPLIT|NOFRAME,$0
	SUBQ	$56, SP
	MOVQ	0x30(GS), AX		// TEB linear address
	MOVL	$0, 0x68(AX)		// TEB.LastErrorValue = 0 (SetLastError(0))
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
	MOVQ	0x30(GS), CX		// TEB linear address
	MOVL	0x68(CX), CX		// TEB.LastErrorValue
	get_tls(DX)
	MOVQ	g(DX), DX
	MOVQ	g_m(DX), DX
	MOVL	CX, (m_mOS+mOS_ntLastError)(DX)
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
// Last-error bracket: same discipline as ntcall6 (zero TEB slot
// before, capture into mOS.ntLastError after).
TEXT runtime·ntcall10(SB),NOSPLIT|NOFRAME,$0
	SUBQ	$88, SP
	MOVQ	0x30(GS), AX		// TEB linear address
	MOVL	$0, 0x68(AX)		// TEB.LastErrorValue = 0 (SetLastError(0))
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
	MOVQ	0x30(GS), CX		// TEB linear address
	MOVL	0x68(CX), CX		// TEB.LastErrorValue
	get_tls(DX)
	MOVQ	g(DX), DX
	MOVQ	g_m(DX), DX
	MOVL	CX, (m_mOS+mOS_ntLastError)(DX)
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
// dies with the thread at ExitThread. Chunk D1: after the pivot the
// NT_TIB StackBase/StackLimit (TEB+0x08/+0x10) are rewritten to the
// WIDE window (policy + wine evidence: os_cosmo_nt_sig.go and
// DEBUGGING.md chunk D1) so exception dispatch/continue validity
// checks accept every stack Go code can run on - the wave-1 stale
// bounds described the abandoned CreateThread stack, and wine refuses
// exception paths whose RSP lies outside the TEB window.
// m.procid is filled by minit.
TEXT runtime·tstart_cosmo_nt(SB),NOSPLIT|NOFRAME,$0
	MOVQ	CX, R13			// mp (same register the clone child uses)
	MOVQ	m_g0(R13), R9		// g0

	// Pivot onto g0's stack.
	MOVQ	(g_stack+stack_hi)(R9), SI
	ANDQ	$~15, SI
	MOVQ	SI, SP

	// TEB stack-bounds hygiene: NT_TIB.StackBase/.StackLimit = the
	// wide window (TEB linear address from the TEB+0x30 self
	// pointer). Constants shared with ntSetTEBStackBounds callers
	// via go_asm.h.
	MOVQ	0x30(GS), AX
	MOVQ	$const__NT_TEB_WIDE_BASE, DX
	MOVQ	DX, 0x08(AX)		// NT_TIB.StackBase (high)
	MOVQ	$const__NT_TEB_WIDE_LIMIT, DX
	MOVQ	DX, 0x10(AX)		// NT_TIB.StackLimit (low)

	// Set up new stack (the clone-child sequence).
	get_tls(CX)
	MOVQ	R13, g_m(R9)
	MOVQ	R9, g(CX)		// gs:0x28 = g0
	MOVQ	R9, R14			// set g register
	CALL	runtime·stackcheck(SB)

	CALL	runtime·mstart(SB)
	INT	$3	// mstart never returns

// func ntSetTEBStackBounds(hi, lo uintptr)
//
// Writes this thread's NT_TIB StackBase (TEB+0x08) and StackLimit
// (TEB+0x10). The kernel and ntdll consult these during exception
// dispatch/continue and unwind sanity checks; the fork installs a
// deliberately wide window (policy note in os_cosmo_nt_sig.go,
// evidence in DEBUGGING.md "Wave 2 chunk D1").
TEXT runtime·ntSetTEBStackBounds(SB),NOSPLIT,$0-16
	MOVQ	0x30(GS), AX		// TEB linear address (NT_TIB.Self)
	MOVQ	hi+0(FP), CX
	MOVQ	CX, 0x08(AX)		// NT_TIB.StackBase
	MOVQ	lo+8(FP), CX
	MOVQ	CX, 0x10(AX)		// NT_TIB.StackLimit
	RET

// func ntGetTEBStackBounds() (hi, lo uintptr)
TEXT runtime·ntGetTEBStackBounds(SB),NOSPLIT,$0-16
	MOVQ	0x30(GS), AX
	MOVQ	0x08(AX), CX
	MOVQ	CX, hi+0(FP)
	MOVQ	0x10(AX), CX
	MOVQ	CX, lo+8(FP)
	RET

// ntsigtramp is the common body of the three exception-callback
// thunks. Win64 entry from the NT exception dispatcher: CX =
// EXCEPTION_POINTERS*, DX = callback kind (loaded by the registration
// thunks below). Modeled on upstream sys_windows_amd64.s sigtramp: it
// saves every register that is callee-saved in the win64 ABI but
// scratch in the Go ABI (the cosmo build's PUSH_REGS_HOST_TO_ABI0
// compiles in its SysV flavor, which under-saves for win64 - DI, SI
// and X6-X15 must be preserved here too), establishes the Go register
// environment (g in R14, X15 zeroed, DF clear), and calls
// runtime.ntSigtrampGo(ep, kind) via its ABI0 wrapper. The int32
// verdict is returned to the dispatcher in EAX.
//
// Runs on the faulting thread's current stack (goroutine stacks
// included - stackSystem reserves the dispatch headroom); the Go side
// hops to g0 via systemstack.
TEXT ntsigtramp<>(SB),NOSPLIT|NOFRAME,$0
	PUSHFQ
	CLD
	PUSHQ	BP
	PUSHQ	BX
	PUSHQ	DI
	PUSHQ	SI
	PUSHQ	R12
	PUSHQ	R13
	PUSHQ	R14
	PUSHQ	R15
	SUBQ	$184, SP	// 0..23 args/ret, 24..183 X6-X15
	MOVUPS	X6, 24(SP)
	MOVUPS	X7, 40(SP)
	MOVUPS	X8, 56(SP)
	MOVUPS	X9, 72(SP)
	MOVUPS	X10, 88(SP)
	MOVUPS	X11, 104(SP)
	MOVUPS	X12, 120(SP)
	MOVUPS	X13, 136(SP)
	MOVUPS	X14, 152(SP)
	MOVUPS	X15, 168(SP)

	// Go ABI environment: g in R14 (nil on a thread that never ran
	// Go code - the nosplit ntSigtrampGo checks), X15 zeroed.
	get_tls(AX)
	MOVQ	g(AX), R14
	PXOR	X15, X15

	MOVQ	CX, 0(SP)	// ep
	MOVL	DX, 8(SP)	// kind
	CALL	runtime·ntSigtrampGo(SB)
	MOVL	16(SP), AX	// verdict (int32, zero-extended)

	MOVUPS	24(SP), X6
	MOVUPS	40(SP), X7
	MOVUPS	56(SP), X8
	MOVUPS	72(SP), X9
	MOVUPS	88(SP), X10
	MOVUPS	104(SP), X11
	MOVUPS	120(SP), X12
	MOVUPS	136(SP), X13
	MOVUPS	152(SP), X14
	MOVUPS	168(SP), X15
	ADDQ	$184, SP
	POPQ	R15
	POPQ	R14
	POPQ	R13
	POPQ	R12
	POPQ	SI
	POPQ	DI
	POPQ	BX
	POPQ	BP
	POPFQ
	RET

TEXT runtime·ntExceptionTramp(SB),NOSPLIT|NOFRAME,$0
	MOVL	$const_ntCallbackVEH, DX
	JMP	ntsigtramp<>(SB)

TEXT runtime·ntFirstVCHTramp(SB),NOSPLIT|NOFRAME,$0
	MOVL	$const_ntCallbackFirstVCH, DX
	JMP	ntsigtramp<>(SB)

TEXT runtime·ntLastVCHTramp(SB),NOSPLIT|NOFRAME,$0
	MOVL	$const_ntCallbackLastVCH, DX
	JMP	ntsigtramp<>(SB)

// func ntExitEncoded(sig uint32)
//
// ExitProcess(0xC0DE0000|sig&0x7F): the fork-private signal-death
// exit status (DEBUGGING.md chunk B wait-status protocol). Also the
// target of the raise/raiseproc NT branches in sys_cosmo_amd64.s
// (same signature, tail JMP). Direct win64 call from a Go stack, so
// SP is realigned per the chunk-B discipline; never returns.
TEXT runtime·ntExitEncoded(SB),NOSPLIT,$0-4
	MOVL	sig+0(FP), CX
	ANDL	$0x7F, CX
	ORL	$0xC0DE0000, CX
	MOVQ	runtime·ntExitProcessFn(SB), AX
	ANDQ	$~15, SP
	SUBQ	$32, SP
	CALL	AX
	INT	$3	// unreachable

// func ntCtrlTramp()
//
// SetConsoleCtrlHandler callback (chunk D2; mapping widened in wave 3
// item 4). Win64 entry: CX = dwCtrlType. Windows runs this on an
// INJECTED foreign thread - no g, no TLS - so it must stay free of Go
// and of ntcall (asmcgocall needs a g): classify the event, OR the
// signal bit into ntCtrlMask, wake the relay M through its dedicated
// event (never the netpoll wake socket), and either report "handled"
// (the keyboard chords CTRL_C -> SIGINT and CTRL_BREAK -> SIGQUIT -
// the relay owns the outcome, including default death) or block
// forever (the process-lifetime events CLOSE -> SIGHUP and LOGOFF/
// SHUTDOWN -> SIGTERM - Windows kills the process the moment such a
// handler returns; blocking gives the Go handlers the OS grace
// window, upstream ctrlHandler's block()). The BREAK -> SIGQUIT and
// CLOSE -> SIGHUP mappings deliberately diverge from upstream Go's
// windows ctrlHandler for unix parity - rationale in DEBUGGING.md
// wave 3 item 4. Direct win64 calls with the chunk-B alignment
// discipline; only volatile registers are used, so the win64
// callee-saved set is preserved by construction.
TEXT runtime·ntCtrlTramp(SB),NOSPLIT|NOFRAME,$0
	SUBQ	$40, SP		// 32B shadow + realign (entry SP == 8 mod 16)
	CMPL	CX, $0
	JE	ctrl_int	// CTRL_C_EVENT(0) -> SIGINT
	CMPL	CX, $1
	JE	ctrl_quit	// CTRL_BREAK_EVENT(1) -> SIGQUIT
	CMPL	CX, $2
	JE	ctrl_hup	// CTRL_CLOSE_EVENT(2) -> SIGHUP, block
	CMPL	CX, $5
	JE	ctrl_term	// CTRL_LOGOFF_EVENT(5) -> SIGTERM, block
	CMPL	CX, $6
	JE	ctrl_term	// CTRL_SHUTDOWN_EVENT(6) -> SIGTERM, block
	XORL	AX, AX		// not ours: FALSE -> next handler
	ADDQ	$40, SP
	RET
ctrl_int:
	MOVQ	$runtime·ntCtrlMask(SB), R8
	LOCK
	ORL	$4, (R8)	// 1<<_SIGINT(2)
	JMP	ctrl_wake_ret
ctrl_quit:
	MOVQ	$runtime·ntCtrlMask(SB), R8
	LOCK
	ORL	$8, (R8)	// 1<<_SIGQUIT(3)
ctrl_wake_ret:
	MOVQ	runtime·ntCtrlEvent(SB), CX
	MOVQ	runtime·ntSetEventFn(SB), AX
	CALL	AX
	MOVL	$1, AX		// TRUE: handled
	ADDQ	$40, SP
	RET
ctrl_hup:
	MOVQ	$runtime·ntCtrlMask(SB), R8
	LOCK
	ORL	$2, (R8)	// 1<<_SIGHUP(1)
	JMP	ctrl_wake_block
ctrl_term:
	MOVQ	$runtime·ntCtrlMask(SB), R8
	LOCK
	ORL	$0x8000, (R8)	// 1<<_SIGTERM(15)
ctrl_wake_block:
	MOVQ	runtime·ntCtrlEvent(SB), CX
	MOVQ	runtime·ntSetEventFn(SB), AX
	CALL	AX
ctrl_block:
	MOVQ	$0xFFFFFFFF, CX	// INFINITE
	MOVQ	runtime·ntSleepFn(SB), AX
	CALL	AX
	JMP	ctrl_block

// func ntSignalTramp(fn, sig uintptr, info, ctx unsafe.Pointer, sp uintptr)
//
// Kernel-mimic signal delivery (ntDeliverSelfSignal): switch to sp
// (the top of this M's gsignal stack) and call the recorded handler -
// the runtime's C-ABI sigtramp - with the Linux handler signature
// (DI=sig, SI=info, DX=ctx). sigtramp preserves the SysV callee-saved
// set, so the caller's SP survives in BX. The handler returns
// normally (no rt_sigreturn on NT) and execution continues in the Go
// caller.
TEXT runtime·ntSignalTramp(SB),NOSPLIT,$0-40
	MOVQ	fn+0(FP), AX
	MOVQ	sig+8(FP), DI
	MOVQ	info+16(FP), SI
	MOVQ	ctx+24(FP), DX
	MOVQ	sp+32(FP), CX
	MOVQ	SP, BX
	ANDQ	$~15, CX
	MOVQ	CX, SP
	CALL	AX
	MOVQ	BX, SP
	RET

