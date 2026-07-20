// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Windows NT signals (wave 2 chunk D1): vectored exception handling
// feeding the fork's linux-shaped sigpanic, self-directed signal
// delivery through the real signal trampoline, and the encoded
// signal-death exit status.
//
// Design (full record in DEBUGGING.md "Wave 2 chunk D1"):
//
//   - Hardware faults: a vectored exception handler (ntExceptionTramp,
//     first position) ported from upstream signal_windows.go. Faults
//     with a Go-text PC and a known exception code are translated to
//     LINUX signal numbers; panic-class signals (SEGV/BUS/FPE, per the
//     fork's linux sigtable) record sig/sigcode0/sigcode1/sigpc on the
//     g and rewrite the saved CONTEXT so the faulting goroutine calls
//     sigpanic0 on resume - recover() then works exactly as on Linux.
//     Throw-class codes (breakpoint/illegal instruction), throwsplit
//     contexts, and runtime.abort crash via ntWinthrow. Everything
//     else returns EXCEPTION_CONTINUE_SEARCH; a last vectored CONTINUE
//     handler prints the crash report for exceptions nothing handled.
//
//   - Signal deaths exit with 0xC0DE0000|signo (ExitProcess), the
//     fork-private encoding chunk B's wait4 already decodes into a
//     Linux "killed by signal" status. runtime.raise/raiseproc on NT
//     jump straight to that exit (they are only called on paths that
//     expect the process to die: dieFromSignal, raisebadsignal).
//
//   - Self-directed signals (kill/tkill/tgkill with the caller as
//     target): the runtime records sigaction state itself (ntSigActs -
//     there is no kernel-side sigaction on NT) and ntKillSelf performs
//     the kernel's decision tree: ignored/default-ignored signals are
//     dropped, uncatchable/default-terminate signals exit encoded, and
//     signals with the Go handler installed are DELIVERED by running
//     the real trampoline (sigtramp -> sigtrampgo -> sighandler) on
//     the calling thread's gsignal stack with a synthesized
//     linux-format siginfo/ucontext - os/signal's Notify pipeline
//     observes them exactly as on Linux, and unwatched fatal signals
//     die through the ordinary dieFromSignal path.
//
//   - Signals aimed at a spawned child terminate it with the encoded
//     status via the stored process handle (TerminateProcess); the
//     parent's wait4 then reports "killed by signal N". kill(-pgid)
//     reaches a child spawned as its own group leader
//     (CREATE_NEW_PROCESS_GROUP) via GenerateConsoleCtrlEvent for
//     SIGINT/SIGQUIT, degrading to leader TerminateProcess for other
//     signals (wave 3 item 4). Unknown pids/pgids are ESRCH.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/sys"
	"unsafe"
)

// ---- win64 exception structures ----

// ntExceptionRecord is EXCEPTION_RECORD (x64: 152 bytes).
type ntExceptionRecord struct {
	exceptionCode        uint32
	exceptionFlags       uint32
	exceptionRecord      *ntExceptionRecord
	exceptionAddress     uintptr
	numberParameters     uint32
	exceptionInformation [15]uintptr // 8-aligned; implicit 4-byte pad before
}

// ntM128A is the win64 M128A (16 bytes).
type ntM128A struct {
	low  uint64
	high uint64
}

// ntContext is the FULL CONTEXT (x64) layout, 1232 (0x4D0) bytes.
// Offsets match upstream
// internal/runtime/syscall/windows/defs_windows_amd64.go (Rip = 0xF8).
// The VEH handlers only touch fields up to rip on OS-allocated
// records, but ntPreemptM (chunk D2) allocates its own buffer for
// GetThreadContext, which requires the complete struct - and a
// 16-byte-aligned base, which Go's 8-byte struct alignment does not
// give; ntPreemptM over-allocates and rounds, upstream's idiom.
type ntContext struct {
	p1home, p2home, p3home, p4home, p5home, p6home uint64
	contextFlags                                   uint32
	mxcsr                                          uint32
	segCs, segDs, segEs, segFs, segGs, segSs       uint16
	eflags                                         uint32
	dr0, dr1, dr2, dr3, dr6, dr7                   uint64
	rax, rcx, rdx, rbx, rsp, rbp, rsi, rdi         uint64
	r8, r9, r10, r11, r12, r13, r14, r15           uint64
	rip                                            uint64
	fltsave                                        [512]byte // XSAVE_FORMAT (legacy FP area)
	vectorRegister                                 [26]ntM128A
	vectorControl                                  uint64
	debugControl                                   uint64
	lastBranchToRip                                uint64
	lastBranchFromRip                              uint64
	lastExceptionToRip                             uint64
	lastExceptionFromRip                           uint64
}

// ntExceptionPointers is EXCEPTION_POINTERS.
type ntExceptionPointers struct {
	record  *ntExceptionRecord
	context *ntContext
}

const (
	_NT_EXCEPTION_ACCESS_VIOLATION     = 0xc0000005
	_NT_EXCEPTION_IN_PAGE_ERROR        = 0xc0000006
	_NT_EXCEPTION_ILLEGAL_INSTRUCTION  = 0xc000001d
	_NT_EXCEPTION_FLT_DENORMAL_OPERAND = 0xc000008d
	_NT_EXCEPTION_FLT_DIVIDE_BY_ZERO   = 0xc000008e
	_NT_EXCEPTION_FLT_INEXACT_RESULT   = 0xc000008f
	_NT_EXCEPTION_FLT_OVERFLOW         = 0xc0000091
	_NT_EXCEPTION_FLT_UNDERFLOW        = 0xc0000093
	_NT_EXCEPTION_INT_DIVIDE_BY_ZERO   = 0xc0000094
	_NT_EXCEPTION_INT_OVERFLOW         = 0xc0000095
	_NT_EXCEPTION_BREAKPOINT           = 0x80000003

	_NT_EXCEPTION_CONTINUE_EXECUTION = -1
	_NT_EXCEPTION_CONTINUE_SEARCH    = 0

	_NT_SEM_FAILCRITICALERRORS    = 0x0001
	_NT_SEM_NOGPFAULTERRORBOX     = 0x0002
	_NT_SEM_NOOPENFILEERRORBOX    = 0x8000
	_NT_WER_FAULT_REPORTING_NO_UI = 0x0020

	// Fork-private signal-death exit status base (DEBUGGING.md chunk
	// B wait-status protocol): a process that dies of signal N exits
	// with 0xC0DE0000|N, which wait4 decodes into the Linux "killed
	// by signal N" status. Must match ntExitEncoded's asm.
	_NT_SIGDEATH_BASE = 0xC0DE0000
)

// TEB stack-bounds policy: a deliberately WIDE NT_TIB window covering
// the whole user address space, installed on the boot thread
// (ntInitSignals) and on every CreateThread thread after its stack
// pivot (tstart_cosmo_nt). Rationale + wine evidence in DEBUGGING.md
// "Wave 2 chunk D1": Go code runs on heap-allocated stacks that move
// (user goroutines) or are Go-allocated (g0s), so no per-thread real
// range can cover every RSP the exception dispatch and
// continue/unwind validity checks will see; a wide window makes every
// live stack "within system stack limits", which removes the need for
// upstream's sigresume workaround (signal_windows.go:174-192) - the
// modified CONTEXT can resume straight onto the faulting goroutine
// stack. Go stacks have no guard pages, so the kernel's guard-based
// stack growth machinery never consults these fields; the only
// remaining consumers are exception dispatch/continue and foreign SEH
// frame validation, all of which the wide window satisfies.
const (
	_NT_TEB_WIDE_BASE  = 0x00007FFFFFFF0000 // highest user-mode address (exclusive-ish)
	_NT_TEB_WIDE_LIMIT = 0x10000            // above the NULL-guard region
)

// Implemented in sys_cosmo_nt_amd64.s.
func ntSetTEBStackBounds(hi, lo uintptr)
func ntGetTEBStackBounds() (hi, lo uintptr)
func ntExceptionTramp()
func ntFirstVCHTramp()
func ntLastVCHTramp()
func ntExitEncoded(sig uint32)

// ntSignalTramp's pointer arguments are consumed synchronously by the
// handler call and never retained (the signal machinery treats them
// as borrowed kernel memory, same as a real delivery).
//
//go:noescape
func ntSignalTramp(fn, sig uintptr, info, ctx unsafe.Pointer, sp uintptr)

// Callback kinds passed by the registration thunks (asm) to
// ntSigtrampGo. Values are shared with sys_cosmo_nt_amd64.s via
// go_asm.h.
const (
	ntCallbackVEH = iota
	ntCallbackFirstVCH
	ntCallbackLastVCH
)

// ntInitSignals registers the exception machinery at NT boot: error
// dialogs off (CI must never hang on a WER popup), the vectored
// exception handler in first position, the first/last vectored
// continue handlers (upstream initExceptionHandler's amd64 shape), and
// the wide TEB stack window for the boot thread (created threads get
// theirs in tstart_cosmo_nt).
func ntInitSignals() {
	em := ntcall(ntGetErrorModeFn, 0, 0, 0, 0, 0, 0)
	ntcall(ntSetErrorModeFn, em|_NT_SEM_FAILCRITICALERRORS|_NT_SEM_NOGPFAULTERRORBOX|_NT_SEM_NOOPENFILEERRORBOX, 0, 0, 0, 0, 0)
	if ntWerGetFlagsFn != 0 && ntWerSetFlagsFn != 0 {
		// Best-effort (wine lacks WerGetFlags): fault-reporting UI
		// off even if WER is later enabled.
		var werflags uintptr
		ntcall(ntWerGetFlagsFn, _NT_CURRENT_PROCESS, uintptr(unsafe.Pointer(&werflags)), 0, 0, 0, 0)
		ntcall(ntWerSetFlagsFn, werflags|_NT_WER_FAULT_REPORTING_NO_UI, 0, 0, 0, 0, 0)
	}

	ntcall(ntAddVectoredExceptionHandlerFn, 1, abi.FuncPCABI0(ntExceptionTramp), 0, 0, 0, 0)
	ntcall(ntAddVectoredContinueHandlerFn, 1, abi.FuncPCABI0(ntFirstVCHTramp), 0, 0, 0, 0)
	ntcall(ntAddVectoredContinueHandlerFn, 0, abi.FuncPCABI0(ntLastVCHTramp), 0, 0, 0, 0)

	ntSetTEBStackBounds(_NT_TEB_WIDE_BASE, _NT_TEB_WIDE_LIMIT)
}

// ntExcToLinuxSig translates an NT exception code to the Linux signal
// number (and siginfo si_code value) the fork's linux-shaped sigpanic
// expects in gp.sig/gp.sigcode0. sig == 0 means "not a code we
// handle". The handled set matches upstream isgoexception
// (signal_windows.go:84-98).
//
//go:nosplit
func ntExcToLinuxSig(code uint32) (sig uint32, code0 uintptr) {
	switch code {
	case _NT_EXCEPTION_ACCESS_VIOLATION:
		return _SIGSEGV, _SEGV_MAPERR
	case _NT_EXCEPTION_IN_PAGE_ERROR:
		return _SIGBUS, _BUS_ADRERR
	case _NT_EXCEPTION_INT_DIVIDE_BY_ZERO:
		return _SIGFPE, _FPE_INTDIV
	case _NT_EXCEPTION_INT_OVERFLOW:
		return _SIGFPE, _FPE_INTOVF
	case _NT_EXCEPTION_FLT_DIVIDE_BY_ZERO:
		return _SIGFPE, _FPE_FLTDIV
	case _NT_EXCEPTION_FLT_OVERFLOW:
		return _SIGFPE, _FPE_FLTOVF
	case _NT_EXCEPTION_FLT_UNDERFLOW:
		return _SIGFPE, _FPE_FLTUND
	case _NT_EXCEPTION_FLT_INEXACT_RESULT:
		return _SIGFPE, _FPE_FLTRES
	case _NT_EXCEPTION_FLT_DENORMAL_OPERAND:
		return _SIGFPE, _FPE_FLTINV
	case _NT_EXCEPTION_BREAKPOINT:
		return _SIGTRAP, 0
	case _NT_EXCEPTION_ILLEGAL_INSTRUCTION:
		return _SIGILL, 0
	}
	return 0, 0
}

// ntIsGoException reports whether this exception should be translated
// into a Go panic or throw: the faulting PC must be inside the Go text
// segment (DLL faults are passed on) and the code must be in the
// handled set. Nosplit like upstream isgoexception.
//
//go:nosplit
func ntIsGoException(info *ntExceptionRecord, r *ntContext) bool {
	pc := uintptr(r.rip)
	if pc < firstmoduledata.text || firstmoduledata.etext < pc {
		return false
	}
	sig, _ := ntExcToLinuxSig(info.exceptionCode)
	return sig != 0
}

// ntIsAbort reports whether the context describes a fault raised by
// runtime.abort (INT3). On NT the reported RIP is one byte AFTER the
// INT3, unlike unix hosts (upstream isAbort, signal_windows.go:58-66).
//
//go:nosplit
func ntIsAbort(r *ntContext) bool {
	return isAbortPC(uintptr(r.rip) - 1)
}

// ntSigtrampGo is called (via the asm thunks) from the NT exception
// dispatcher. Nosplit: no stack growth until the abort/throwsplit
// checks have run.
//
//go:nosplit
func ntSigtrampGo(ep *ntExceptionPointers, kind int32) int32 {
	gp := getg()
	if gp == nil {
		// Exception on a thread that never ran Go code (none exist
		// in wave 2, but SetConsoleCtrlHandler-style injected threads
		// would arrive here): not ours.
		return _NT_EXCEPTION_CONTINUE_SEARCH
	}

	var fn func(info *ntExceptionRecord, r *ntContext, gp *g) int32
	switch kind {
	case ntCallbackVEH:
		fn = ntExceptionHandler
	case ntCallbackFirstVCH:
		fn = ntFirstContinueHandler
	case ntCallbackLastVCH:
		fn = ntLastContinueHandler
	default:
		throw("ntSigtrampGo: unknown callback kind")
	}

	// Run the handler on g0 (upstream sigtrampgo's shape). No
	// sigresume dance afterwards: the TEB stack window is wide (see
	// the policy note above), so a resume SP on any live Go stack
	// already lies "within system stack limits".
	var ret int32
	if gp != gp.m.g0 {
		systemstack(func() {
			ret = fn(ep.record, ep.context, gp)
		})
	} else {
		ret = fn(ep.record, ep.context, gp)
	}
	return ret
}

// ntExceptionHandler is the vectored exception handler body (upstream
// exceptionhandler, signal_windows.go:203-247, with the NT->Linux
// signal translation and the fork's linux sigtable driving the
// panic-vs-throw split).
//
//go:nosplit
func ntExceptionHandler(info *ntExceptionRecord, r *ntContext, gp *g) int32 {
	if !ntIsGoException(info, r) {
		return _NT_EXCEPTION_CONTINUE_SEARCH
	}

	sig, code0 := ntExcToLinuxSig(info.exceptionCode)
	if gp.throwsplit || ntIsAbort(r) || sigtable[sig].flags&_SigPanic == 0 {
		// We can't safely sigpanic (stack may not grow), this is a
		// call to abort, or the signal is throw-class on the fork's
		// linux sigtable (SIGTRAP/SIGILL - Linux never panics
		// those). Crash now.
		ntWinthrow(info, r, gp)
	}

	// After this point it is safe to grow the stack.

	// Pass arguments to sigpanic out of band (augmenting the stack
	// frame would break unwinding), in LINUX shape: gp.sig is the
	// Linux signal number, gp.sigcode0 the siginfo si_code, and
	// gp.sigcode1 the fault address (AV/in-page only; Linux si_addr
	// is only meaningful for SEGV/BUS).
	gp.sig = sig
	gp.sigcode0 = code0
	gp.sigcode1 = 0
	if info.exceptionCode == _NT_EXCEPTION_ACCESS_VIOLATION || info.exceptionCode == _NT_EXCEPTION_IN_PAGE_ERROR {
		gp.sigcode1 = info.exceptionInformation[1]
	}
	gp.sigpc = uintptr(r.rip)

	// Make it look like the faulting code called sigpanic0. Only push
	// the return frame if RIP != 0 (a call through a nil func should
	// trace as a call to sigpanic from the CALLER) and RIP is not the
	// asyncPreempt entry (issue #35773: a preemption injected between
	// the fault and the handler must not be double-framed) - upstream
	// signal_windows.go:227-245.
	if r.rip != 0 && uintptr(r.rip) != abi.FuncPCABI0(asyncPreempt) {
		sp := uintptr(r.rsp) - goarch.PtrSize
		r.rsp = uint64(sp)
		*(*uintptr)(unsafe.Pointer(sp)) = gp.sigpc
	}
	r.rip = uint64(abi.FuncPCABI0(sigpanic0))
	return _NT_EXCEPTION_CONTINUE_EXECUTION
}

// ntFirstContinueHandler stops the vectored continue handler search
// for exceptions our VEH already handled: Windows walks the continue
// handler list even after EXCEPTION_CONTINUE_EXECUTION (upstream
// firstcontinuehandler, signal_windows.go:286-299). Note the check
// still passes here because the rewritten RIP (sigpanic0) is in Go
// text and the exception code is unchanged.
//
//go:nosplit
func ntFirstContinueHandler(info *ntExceptionRecord, r *ntContext, gp *g) int32 {
	if !ntIsGoException(info, r) {
		return _NT_EXCEPTION_CONTINUE_SEARCH
	}
	return _NT_EXCEPTION_CONTINUE_EXECUTION
}

// ntLastContinueHandler is reached when nothing handled the exception
// (upstream lastcontinuehandler; the DLL/archive and arm64 special
// cases do not apply to an APE). Print the crash and die.
//
//go:nosplit
func ntLastContinueHandler(info *ntExceptionRecord, r *ntContext, gp *g) int32 {
	ntWinthrow(info, r, gp)
	return 0 // not reached
}

// ntWinthrow prints the fork's fatal-signal report for an exception
// the runtime cannot turn into a panic, then exits with the encoded
// signal status (upstream winthrow, signal_windows.go:333-374, except
// the exit: RaiseFailFastException would surface the raw NTSTATUS,
// while the 0xC0DE encoding keeps every signal death uniform for
// wait4). Always called on g0 (via ntSigtrampGo's systemstack). gp is
// the g the exception occurred on.
//
//go:nosplit
func ntWinthrow(info *ntExceptionRecord, r *ntContext, gp *g) {
	g0 := getg()

	if panicking.Load() != 0 { // traceback already printed
		exit(2)
	}
	panicking.Store(1)

	// In case we're handling a g0 stack overflow, blow away the g0
	// stack bounds so we have room to print the traceback. If this
	// somehow overflows the stack, the OS will trap it.
	g0.stack.lo = 0
	g0.stackguard0 = g0.stack.lo + stackGuard
	g0.stackguard1 = g0.stackguard0

	sig, _ := ntExcToLinuxSig(info.exceptionCode)
	if sig != 0 && sig < uint32(len(sigtable)) {
		print(sigtable[sig].name, "\n")
	}
	print("Exception ", hex(uintptr(info.exceptionCode)), " ", hex(info.exceptionInformation[0]), " ", hex(info.exceptionInformation[1]), " ", hex(uintptr(r.rip)), "\n")
	print("PC=", hex(uintptr(r.rip)), "\n\n")

	g0.m.throwing = throwTypeRuntime
	g0.m.caughtsig.set(gp)

	level, _, _ := gotraceback()
	if level > 0 {
		tracebacktrap(uintptr(r.rip), uintptr(r.rsp), 0, gp)
		tracebackothers(gp)
		ntDumpregs(r)
	}

	if sig == 0 {
		// A code outside our set reached the last continue handler.
		// Encode the same catch-all chunk B's wait4 uses for unknown
		// NTSTATUS crashes: SIGKILL.
		sig = _SIGKILL
	}
	ntExitEncoded(sig)
}

// ntDumpregs prints the CONTEXT registers (upstream dumpregs,
// signal_windows_amd64.go).
//
//go:nosplit
func ntDumpregs(r *ntContext) {
	print("rax     ", hex(r.rax), "\n")
	print("rbx     ", hex(r.rbx), "\n")
	print("rcx     ", hex(r.rcx), "\n")
	print("rdx     ", hex(r.rdx), "\n")
	print("rdi     ", hex(r.rdi), "\n")
	print("rsi     ", hex(r.rsi), "\n")
	print("rbp     ", hex(r.rbp), "\n")
	print("rsp     ", hex(r.rsp), "\n")
	print("r8      ", hex(r.r8), "\n")
	print("r9      ", hex(r.r9), "\n")
	print("r10     ", hex(r.r10), "\n")
	print("r11     ", hex(r.r11), "\n")
	print("r12     ", hex(r.r12), "\n")
	print("r13     ", hex(r.r13), "\n")
	print("r14     ", hex(r.r14), "\n")
	print("r15     ", hex(r.r15), "\n")
	print("rip     ", hex(r.rip), "\n")
	print("rflags  ", hex(uint64(r.eflags)), "\n")
	print("cs      ", hex(uint64(r.segCs)), "\n")
	print("fs      ", hex(uint64(r.segFs)), "\n")
	print("gs      ", hex(uint64(r.segGs)), "\n")
}

// ---- sigaction recording ----

// ntSigActs is the runtime-side sigaction table: NT has no kernel
// sigaction, and the only installer is the runtime itself (setsig /
// sigenable / sigdisable / sigignore via sysSigaction), so recording
// handler state here is the whole implementation. ntKillSelf consults
// it for the delivery decision. Writes are not atomic: the install
// paths are serialized by construction (initsig at boot; the
// signal_enable/disable channel protocol afterwards), matching the
// unsynchronized-kernel-table semantics Linux provides.
var ntSigActs [_NSIG]sigactiont

// ntSigaction is the NT leg of sysSigaction (os_cosmo.go).
//
//go:nosplit
//go:nowritebarrierrec
func ntSigaction(sig uint32, new, old *sigactiont) int32 {
	if sig == 0 || sig >= _NSIG {
		return -1
	}
	if old != nil {
		*old = ntSigActs[sig]
	}
	if new != nil {
		ntSigActs[sig] = *new
	}
	return 0
}

// ---- kill / tkill / tgkill emulation ----

// ntSigDefaultIgnored reports whether SIG_DFL for sig discards it
// (the Linux kernel's default-ignore set), plus the stop family:
// job control does not exist on NT - nothing could ever deliver the
// SIGCONT that would resume an emulated stop - so stops are dropped.
//
//go:nosplit
func ntSigDefaultIgnored(sig uint32) bool {
	switch sig {
	case _SIGCHLD, _SIGURG, _SIGWINCH, _SIGCONT, _SIGSTOP, _SIGTSTP, _SIGTTIN, _SIGTTOU:
		return true
	}
	return false
}

// GenerateConsoleCtrlEvent ctrl-type ids (winbase.h).
const (
	_NT_CTRL_C_EVENT     = 0
	_NT_CTRL_BREAK_EVENT = 1
)

// ntEmuKill implements kill(2) (dispatcher case, os_cosmo_nt_sys.go).
// pid == self delivers on the calling thread; a pid from the chunk-B
// spawn table terminates that child with the encoded status;
// pid < -1 addresses a process GROUP we created (wave 3 item 4,
// ntEmuKillGroup below). Anything else is ESRCH: unrelated processes
// are not addressable (we only hold handles for our own children),
// and pid == 0 (the caller's own group) and pid == -1 (broadcast)
// have no NT projection - this process is not a group we created, so
// both keep the pre-wave-3 ESRCH.
func ntEmuKill(pid, sig int32) (r1, r2, errno uintptr) {
	if sig < 0 || sig >= _NSIG {
		return ntFail3(ntEINVAL)
	}
	self := int32(uint32(ntcall(ntGetCurrentProcessIdFn, 0, 0, 0, 0, 0, 0)))
	if pid == self {
		if eno := ntKillSelf(uint32(sig)); eno != 0 {
			return ntFail3(eno)
		}
		return 0, 0, 0
	}
	if pid < -1 {
		return ntEmuKillGroup(uint32(-pid), sig)
	}
	if pid <= 0 {
		return ntFail3(ntESRCH)
	}
	h, ok := ntProcFind(uint32(pid))
	if !ok {
		return ntFail3(ntESRCH)
	}
	if sig == 0 {
		return 0, 0, 0 // existence probe
	}
	// Terminate the child with the fork's encoded signal status;
	// chunk B's wait4 decodes it into "killed by signal sig". Best
	// effort: TerminateProcess on an already-exited child fails, and
	// Linux kill on a zombie succeeds, so the result is not
	// surfaced. The handle stays in the table - wait4 still reaps.
	ntcall(ntTerminateProcessFn, h, _NT_SIGDEATH_BASE|uintptr(uint32(sig)), 0, 0, 0, 0)
	return 0, 0, 0
}

// ntEmuKillGroup implements kill(-pgid, sig): signal a whole process
// group. Only groups WE created are addressable - pgid must be the
// pid of a spawned child launched with CREATE_NEW_PROCESS_GROUP
// (SysProcAttr{Setpgid: true} through ntForkExec/ntSpawn); everything
// else is ESRCH, mirroring the own-children-only rule of the
// positive-pid arm.
//
//   - SIGQUIT -> GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pgid):
//     THE reliably deliverable group-targeted console event (upstream
//     Go's own TestCtrlBreak uses exactly this pairing). In a cosmo
//     child the injected handler maps CTRL_BREAK back to SIGQUIT,
//     completing the Linux-shaped round trip.
//   - SIGINT -> GenerateConsoleCtrlEvent(CTRL_C_EVENT, pgid),
//     best-effort: NT creates CREATE_NEW_PROCESS_GROUP children with
//     Ctrl-C DISABLED until they opt back in (SetConsoleCtrlHandler
//     (NULL, FALSE)), so delivery to a child that never re-enabled it
//     silently no-ops. Upstream windows Go has the identical hole;
//     callers wanting a reliable group chord send SIGQUIT.
//   - sig 0 -> existence probe.
//   - any other sig -> TerminateProcess(leader, encoded status): no
//     NT API delivers arbitrary signals group-wide, so group-kill
//     degrades to leader-kill - the leader is the group's one member
//     we know of (documented in DEBUGGING.md wave 3 item 4). Same
//     best-effort result discipline as the positive arm (a dead
//     leader is the Linux kill-a-zombie success).
//
// A GenerateConsoleCtrlEvent failure (e.g. no console attached)
// surfaces as the mapped errno from the trampoline-captured last
// error.
func ntEmuKillGroup(pgid uint32, sig int32) (r1, r2, errno uintptr) {
	h, ok := ntProcFindGroup(pgid)
	if !ok {
		return ntFail3(ntESRCH)
	}
	switch sig {
	case 0:
		return 0, 0, 0 // existence probe
	case _SIGINT, _SIGQUIT:
		ev := uintptr(_NT_CTRL_BREAK_EVENT)
		if sig == _SIGINT {
			ev = _NT_CTRL_C_EVENT
		}
		if r, werr := ntcallE(ntGenerateConsoleCtrlEventFn, ev, uintptr(pgid), 0, 0, 0, 0, 0); r == 0 {
			return ntFail3(ntErrno(werr))
		}
		return 0, 0, 0
	}
	ntcall(ntTerminateProcessFn, h, _NT_SIGDEATH_BASE|uintptr(uint32(sig)), 0, 0, 0, 0)
	return 0, 0, 0
}

// ntEmuTkill implements tkill(2). Only the calling thread is
// addressable in chunk D1: cross-thread delivery needs the
// SuspendThread machinery (chunk D2's preemptM), and every
// process-level observable (os/signal, signal deaths) is
// thread-agnostic anyway. The runtime's own signalM stays gated off
// on NT, so nothing in-tree sends cross-thread.
func ntEmuTkill(tid, sig int32) (r1, r2, errno uintptr) {
	if sig < 0 || sig >= _NSIG {
		return ntFail3(ntEINVAL)
	}
	cur := int32(uint32(ntcall(ntGetCurrentThreadIdFn, 0, 0, 0, 0, 0, 0)))
	if tid != cur {
		return ntFail3(ntESRCH)
	}
	if eno := ntKillSelf(uint32(sig)); eno != 0 {
		return ntFail3(eno)
	}
	return 0, 0, 0
}

// ntEmuTgkill implements tgkill(2): tgid must be this process.
func ntEmuTgkill(tgid, tid, sig int32) (r1, r2, errno uintptr) {
	self := int32(uint32(ntcall(ntGetCurrentProcessIdFn, 0, 0, 0, 0, 0, 0)))
	if tgid != self {
		return ntFail3(ntESRCH)
	}
	return ntEmuTkill(tid, sig)
}

// ntKillSelf performs the kernel's delivery decision for a
// self-directed signal, on the calling thread. Returns a Linux errno
// (0 on success). Runs as ordinary Go in the syscall-emulation
// context (user goroutine).
func ntKillSelf(sig uint32) uintptr {
	if sig == 0 {
		return 0
	}
	if sig == _SIGKILL {
		// Uncatchable: die with the encoded status immediately (the
		// kernel would never consult handlers either). Ordered exit:
		// this is a normal-operation death (waitsig exercises it every
		// probe run), so it takes ntSuspendLock like ntExit.
		ntExitEncodedOrdered(_SIGKILL)
	}
	handler := ntSigActs[sig].sa_handler
	if handler == _SIG_IGN {
		return 0
	}
	if handler == _SIG_DFL {
		if ntSigDefaultIgnored(sig) {
			return 0
		}
		ntExitEncodedOrdered(sig) // default action: terminate
	}
	ntDeliverSelfSignal(sig, handler)
	return 0
}

// ntDeliverSelfSignal runs the recorded handler - in practice always
// the runtime's sigtramp, the only installer on NT - on this thread's
// gsignal stack with a synthesized linux-format siginfo/ucontext,
// mimicking kernel delivery: sigtramp -> sigtrampgo -> sighandler then
// applies the ordinary Linux semantics (sigsend/Notify for watched
// signals, dieFromSignal for unwatched fatal ones - whose raise()
// lands in ntExitEncoded - signal_ignored, throw).
func ntDeliverSelfSignal(sig uint32, handler uintptr) {
	gp := getg()

	var info siginfo
	info.si_signo = int32(sig)
	info.si_code = _SI_TKILL // user-space send: sigFromUser() == true

	// Synthesized context. Only rip/rsp are consulted on the paths a
	// user-sent signal can take (the fatal-signal report, and
	// doSigPreempt's safe-point probe for SIGURG - which always
	// refuses a "runtime." PC, so no context rewriting can happen on
	// this dead context; it is discarded when the handler returns).
	var uc ucontext
	regs := (*sigcontext)(unsafe.Pointer(&uc.uc_mcontext))
	regs.rip = uint64(sys.GetCallerPC())
	regs.rsp = uint64(sys.GetCallerSP())

	// Deliver on the gsignal stack, where the Linux kernel would
	// deliver (minitSignalStack installs gsignal as the alt stack on
	// every M): sigtrampgo's stack accounting then takes its
	// ordinary path. The stack is otherwise unused on NT and the
	// delivery is synchronous, so borrowing it is safe.
	ntSignalTramp(handler, uintptr(sig), unsafe.Pointer(&info), unsafe.Pointer(&uc), gp.m.gsignal.stack.hi)
}
