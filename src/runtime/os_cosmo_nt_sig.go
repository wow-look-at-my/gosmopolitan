// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

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

// Implemented in sys_cosmo_nt_<goarch>.s.
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
// ntSigtrampGo. Values are shared with sys_cosmo_nt_<goarch>.s via
// go_asm.h.
const (
	ntCallbackVEH = iota
	ntCallbackFirstVCH
	ntCallbackLastVCH
)

// ntInitSignals registers the exception machinery at NT boot: error
// dialogs off (CI must never hang on a WER popup), the vectored
// exception handler in first position, the first/last vectored
// continue handlers (upstream initExceptionHandler's shape), and
// the wide TEB stack window for the boot thread (created threads get
// theirs in tstart_cosmo_nt).
func ntInitSignals() {
	// Publish g where the exception trampolines find it. A no-op on
	// amd64, where rt0's TLS setup already wrote the same TEB slot.
	ntSetTEBg()

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
	pc := r.getPC()
	if pc < firstmoduledata.text || firstmoduledata.etext < pc {
		return false
	}
	sig, _ := ntExcToLinuxSig(info.exceptionCode)
	return sig != 0
}

// ntIsAbort reports whether the context describes a fault raised by
// runtime.abort. On amd64 NT reports the RIP one byte AFTER the INT3,
// unlike unix hosts; on arm64 the reported PC is the faulting
// instruction itself (upstream isAbort, signal_windows.go).
//
//go:nosplit
func ntIsAbort(r *ntContext) bool {
	pc := r.getPC()
	if GOARCH == "amd64" {
		pc--
	}
	return isAbortPC(pc)
}

// ntSigtrampGo is called (via the asm thunks) from the NT exception
// dispatcher. Nosplit: no stack growth until the abort/throwsplit
// checks have run.
//
//go:nosplit
func ntSigtrampGo(ep *ntExceptionPointers, kind int32) int32 {
	// g was established by the asm thunk: on amd64 from TLS (gs:0x28),
	// on arm64 from the faulting CONTEXT's x28 when the PC is in Go
	// text and from the TEB slot (this thread's g0) otherwise.
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
	gp.sigpc = r.getPC()

	// Make it look like the faulting code called sigpanic0. Only push
	// the return frame if the PC is nonzero (a call through a nil func
	// should trace as a call to sigpanic from the CALLER) and is not
	// the asyncPreempt entry (issue #35773: a preemption injected
	// between the fault and the handler must not be double-framed) -
	// upstream signal_windows.go:227-245.
	if pc := r.getPC(); pc != 0 && pc != abi.FuncPCABI0(asyncPreempt) {
		r.pushCall(abi.FuncPCABI0(sigpanic0), gp.sigpc)
	} else {
		r.setPC(abi.FuncPCABI0(sigpanic0))
	}
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
// (upstream lastcontinuehandler; the DLL/archive case does not apply to
// an APE). Print the crash and die.
//
//go:nosplit
func ntLastContinueHandler(info *ntExceptionRecord, r *ntContext, gp *g) int32 {
	// arm64 MSVC-built DLLs (the APE loads kernel32, ws2_32, iphlpapi,
	// bcryptprimitives) probe CPU features at load time by trapping
	// illegal instructions under SEH. VEH runs before SEH, so an
	// illegal instruction from non-Go code is that probe: pass it on.
	if GOARCH == "arm64" && info.exceptionCode == _NT_EXCEPTION_ILLEGAL_INSTRUCTION &&
		(r.getPC() < firstmoduledata.text || firstmoduledata.etext < r.getPC()) {
		return _NT_EXCEPTION_CONTINUE_SEARCH
	}
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
	print("Exception ", hex(uintptr(info.exceptionCode)), " ", hex(info.exceptionInformation[0]), " ", hex(info.exceptionInformation[1]), " ", hex(r.getPC()), "\n")
	print("PC=", hex(r.getPC()), "\n\n")

	g0.m.throwing = throwTypeRuntime
	g0.m.caughtsig.set(gp)

	level, _, _ := gotraceback()
	if level > 0 {
		tracebacktrap(r.getPC(), r.getSP(), r.getLR(), gp)
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

// ---- signal mask ----

// ntSigMask is the blocked-signal set, and ntSigPending the signals a
// send found blocked. NT has no kernel signal mask, and the asm
// rtsigprocmask stub used to return success while blocking nothing - so
// a runtime that had just blocked every signal around a critical section
// could still be reentered by one.
//
// Recording it here is the whole implementation, for the same reason
// ntSigActs is: the only sender that reaches a thread on this host is
// the process itself (ntKillSelf), so a set this file consults is a set
// that decides delivery. Unsynchronized like ntSigActs, and for the same
// reason - a thread's own mask is written only by that thread.
var (
	ntSigMask    sigset
	ntSigPending sigset
)

//go:nosplit
func ntSigsetHas(mask *sigset, sig uint32) bool {
	if sig == 0 || sig >= _NSIG {
		return false
	}
	return mask[(sig-1)/32]&(1<<((sig-1)&31)) != 0
}

// ntSigprocmask is the NT leg of sigprocmask (os_cosmo.go). It applies
// the change and then delivers whatever the change unblocked, which is
// what the kernel does at the end of its own sigprocmask.
//
//go:nosplit
func ntSigprocmask(how int32, new, old *sigset) {
	if old != nil {
		*old = ntSigMask
	}
	if new == nil {
		return
	}
	switch how {
	case _SIG_BLOCK:
		ntSigMask[0] |= new[0]
		ntSigMask[1] |= new[1]
	case _SIG_UNBLOCK:
		ntSigMask[0] &^= new[0]
		ntSigMask[1] &^= new[1]
	case _SIG_SETMASK:
		ntSigMask = *new
	default:
		return
	}
	// SIGKILL and SIGSTOP cannot be blocked on Linux either.
	sigdelset(&ntSigMask, _SIGKILL)
	sigdelset(&ntSigMask, _SIGSTOP)
	ntFlushPendingSignals()
}

// ntFlushPendingSignals delivers signals that arrived while they were
// blocked and are not blocked any more.
//
// Only from a user goroutine: delivery runs a handler on the gsignal
// stack, and the boot and signal-handling paths that call sigprocmask on
// g0 must not re-enter it. A signal stays pending until the next
// unblock from ordinary Go code, which is where every caller that can
// receive one already is.
//
//go:nosplit
func ntFlushPendingSignals() {
	gp := getg()
	if gp == nil || gp.m == nil || gp == gp.m.g0 || gp == gp.m.gsignal {
		return
	}
	for sig := uint32(1); sig < _NSIG; sig++ {
		if !ntSigsetHas(&ntSigPending, sig) || ntSigsetHas(&ntSigMask, sig) {
			continue
		}
		sigdelset(&ntSigPending, int(sig))
		// The decision tree runs again rather than the handler being
		// called directly: SIG_IGN may have been installed while the
		// signal waited, and a default-terminate signal must still
		// terminate. ntKillSelf may grow the stack, which is why the
		// guard above has to come first - it is only ever reached from a
		// user goroutine.
		ntKillSelf(sig)
	}
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
	// A blocked signal is neither delivered nor discarded: it waits.
	// SIGKILL is already gone above, and nothing else outranks the mask.
	if ntSigsetHas(&ntSigMask, sig) {
		sigaddset(&ntSigPending, int(sig))
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

	// Synthesized context. Only the PC and SP are consulted on the
	// paths a user-sent signal can take (the fatal-signal report, and
	// doSigPreempt's safe-point probe for SIGURG - which always
	// refuses a "runtime." PC, so no context rewriting can happen on
	// this dead context; it is discarded when the handler returns).
	var uc ucontext
	ntSetSyntheticPCSP(&uc, sys.GetCallerPC(), sys.GetCallerSP())

	// Deliver on the gsignal stack, where the Linux kernel would
	// deliver (minitSignalStack installs gsignal as the alt stack on
	// every M): sigtrampgo's stack accounting then takes its
	// ordinary path. The stack is otherwise unused on NT and the
	// delivery is synchronous, so borrowing it is safe.
	ntSignalTramp(handler, uintptr(sig), unsafe.Pointer(&info), unsafe.Pointer(&uc), gp.m.gsignal.stack.hi)
}
