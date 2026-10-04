// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// Windows NT signals: vectored exception handling feeding the fork's
// linux-shaped sigpanic, self-directed delivery through the real signal
// trampoline, and the encoded signal-death exit status.
//
// NT has no kernel-side sigaction, so the runtime records the disposition
// itself in ntSigActs and ntKillSelf runs the kernel's decision tree over it.
// Signals aimed at a spawned child go through os_cosmo_nt_kill.go instead.

package runtime

import (
	"internal/goarch"
	"internal/abi"
	"internal/runtime/sys"
	"unsafe"
)

// ---- win64 exception structures ----

// ntExceptionRecord is EXCEPTION_RECORD (x64: many bytes).
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

	// Fork-private signal-death exit status base: a process that dies of signal N exits with 0xC0DE0000|N.
	_NT_SIGDEATH_BASE = 0xC0DE0000
)

// The NT_TIB stack window is deliberately WIDE, covering the whole user
// address space.
const (
	_NT_TEB_WIDE_BASE  = 0x00007FFFFFFF0000 // highest user-mode address (exclusive-ish)
	_NT_TEB_WIDE_LIMIT = 0x10000            // above the NULL-guard region
)

// Implemented in sys_cosmo_nt_<goarch>.s.
func ntSetTEBStackBounds(hi, lo uintptr)
func ntGetTEBStackBounds() (hi, lo uintptr)

// ntExceptionTramp is the first-position vectored exception handler,.go.
func ntExceptionTramp()
func ntFirstVCHTramp()
func ntLastVCHTramp()
func ntExitEncoded(sig uint32)

//go:noescape
func ntSignalTramp(fn, sig uintptr, info, ctx unsafe.Pointer, sp uintptr)

// Callback kinds passed by the registration thunks (asm) to ntSigtrampGo.
// Values are shared with sys_cosmo_nt_<goarch>.s via go_asm.h.
const (
	ntCallbackVEH = iota
	ntCallbackFirstVCH
	ntCallbackLastVCH
)

// ntInitSignals registers the exception machinery at NT boot: error dialogs
// off (CI must never hang on a WER popup), the vectored exception handler in
// first position, the first/last vectored continue handlers (upstream
// initExceptionHandler's shape), and the wide TEB stack window for the boot
// thread (created threads get theirs in tstart_cosmo_nt).
func ntInitSignals() {
	// Publish g where the exception trampolines find it.
	ntSetTEBg()

	em := ntcall(ntGetErrorModeFn, 0, 0, 0, 0, 0, 0)
	ntcall(ntSetErrorModeFn, em|_NT_SEM_FAILCRITICALERRORS|_NT_SEM_NOGPFAULTERRORBOX|_NT_SEM_NOOPENFILEERRORBOX, 0, 0, 0, 0, 0)
	if ntWerGetFlagsFn != 0 && ntWerSetFlagsFn != 0 {
		// Best-effort (wine lacks WerGetFlags): fault-reporting UI off even if WER is later enabled.
		var werflags uintptr
		ntcall(ntWerGetFlagsFn, _NT_CURRENT_PROCESS, uintptr(unsafe.Pointer(&werflags)), 0, 0, 0, 0)
		ntcall(ntWerSetFlagsFn, werflags|_NT_WER_FAULT_REPORTING_NO_UI, 0, 0, 0, 0, 0)
	}

	ntcall(ntAddVectoredExceptionHandlerFn, 1, abi.FuncPCABI0(ntExceptionTramp), 0, 0, 0, 0)
	ntcall(ntAddVectoredContinueHandlerFn, 1, abi.FuncPCABI0(ntFirstVCHTramp), 0, 0, 0, 0)
	ntcall(ntAddVectoredContinueHandlerFn, 0, abi.FuncPCABI0(ntLastVCHTramp), 0, 0, 0, 0)

	ntSetTEBStackBounds(_NT_TEB_WIDE_BASE, _NT_TEB_WIDE_LIMIT)
}

// ntExcToLinuxSig translates an NT exception code to the Linux signal number
// (and siginfo si_code value) the fork's linux-shaped sigpanic expects in
// gp.sig/gp.sigcode0. sig == 0 means "not a code we handle". The handled set
// matches upstream isgoexception (signal_windows.go:84-98).
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

// ntIsGoException reports whether this exception should be translated into a
// Go panic or throw: the faulting PC must be inside the Go text segment (DLL
// faults are passed on) and the code must be in the handled set.
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
// runtime.abort.
//
//go:nosplit
func ntIsAbort(r *ntContext) bool {
	pc := r.getPC()
	if goarch.IsAmd64 == 1 {
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
	// g was established by the asm thunk: on amd64 from TLS (gs:0x28).
	gp := getg()
	if gp == nil {
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

	// Run the handler on g0 (upstream sigtrampgo's shape).
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
		// We cannot safely sigpanic (stack may not grow).
		ntWinthrow(info, r, gp)
	}

	// After this point it is safe to grow the stack.

	// Pass arguments to sigpanic out of band (augmenting the stack frame would break unwinding), in LINUX shape.
	gp.sig = sig
	gp.sigcode0 = code0
	gp.sigcode1 = 0
	if info.exceptionCode == _NT_EXCEPTION_ACCESS_VIOLATION || info.exceptionCode == _NT_EXCEPTION_IN_PAGE_ERROR {
		gp.sigcode1 = info.exceptionInformation[1]
	}
	gp.sigpc = r.getPC()

	// Make it look like the faulting code called sigpanic0.
	if pc := r.getPC(); pc != 0 && pc != abi.FuncPCABI0(asyncPreempt) {
		r.pushCall(abi.FuncPCABI0(sigpanic0), gp.sigpc)
	} else {
		r.setPC(abi.FuncPCABI0(sigpanic0))
	}
	return _NT_EXCEPTION_CONTINUE_EXECUTION
}

// ntFirstContinueHandler stops the vectored continue handler search for
// exceptions our VEH already handled.
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
	// bcryptprimitives) probe CPU features at load time by trapping illegal
	// instructions under SEH.
	if goarch.IsArm64 == 1 && info.exceptionCode == _NT_EXCEPTION_ILLEGAL_INSTRUCTION &&
		(r.getPC() < firstmoduledata.text || firstmoduledata.etext < r.getPC()) {
		return _NT_EXCEPTION_CONTINUE_SEARCH
	}
	ntWinthrow(info, r, gp)
	return 0 // not reached
}

// ntWinthrow prints the fork's fatal-signal report for an exception the
// runtime cannot turn into a panic, then exits with the encoded signal status
// (upstream winthrow, signal_windows.go:333-374, except the exit:
// RaiseFailFastException would surface the raw NTSTATUS, while the 0xC0DE
// encoding keeps every signal death uniform for wait4). Always called on g0
// (via ntSigtrampGo's systemstack). gp is the g the exception occurred on.
//
//go:nosplit
func ntWinthrow(info *ntExceptionRecord, r *ntContext, gp *g) {
	g0 := getg()

	// One line past the print machinery first: a fault inside the report below would otherwise leave nothing.
	ntWinthrowLine(info, r, gp, panicking.Load() != 0)
	if panicking.Load() != 0 {
		exit(2)
	}
	panicking.Store(1)

	// In case we're handling a g0 stack overflow, blow away the g0 stack bounds so we have room to print the traceback.
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
		sig = _SIGKILL
	}
	ntExitEncoded(sig)
}

// ---- sigaction recording ----

// ntSigActs is the runtime-side sigaction table: NT has no kernel sigaction.
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

// ntSigMask is the blocked-signal set, and ntSigPending the signals a send
// found blocked.
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

// ntFlushPendingSignals delivers signals that arrived while they were blocked
// and are not blocked any more.
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
		// The decision tree runs again rather than the handler being called directly: SIG_IGN may have been installed while the signal waited.
		ntKillSelf(sig)
	}
}

// ---- kill / tkill / tgkill emulation ----

// ntSigDefaultIgnored reports whether SIG_DFL for sig discards it (the Linux
// kernel's default-ignore set), plus the stop family.
//
//go:nosplit
func ntSigDefaultIgnored(sig uint32) bool {
	switch sig {
	case _SIGCHLD, _SIGURG, _SIGWINCH, _SIGCONT, _SIGSTOP, _SIGTSTP, _SIGTTIN, _SIGTTOU:
		return true
	}
	return false
}

// ntKillSelf performs the kernel's delivery decision for a self-directed
// signal, on the calling thread. Returns a Linux errno (0 on success). Runs
// as ordinary Go in the syscall-emulation context (user goroutine).
func ntKillSelf(sig uint32) uintptr {
	if sig == 0 {
		return 0
	}
	if sig == _SIGKILL {
		// Uncatchable: die with the encoded status immediately (the kernel would never consult handlers either).
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

// ntDeliverSelfSignal runs the recorded handler - in practice always the
// runtime's sigtramp, the only installer on NT - on this thread's gsignal
// stack with a synthesized linux-format siginfo/ucontext, mimicking kernel
// delivery.
func ntDeliverSelfSignal(sig uint32, handler uintptr) {
	gp := getg()

	var info siginfo
	info.si_signo = int32(sig)
	info.si_code = _SI_TKILL // user-space send: sigFromUser() == true

	// Synthesized context.
	var uc ucontext
	ntSetSyntheticPCSP(&uc, sys.GetCallerPC(), sys.GetCallerSP())

	// Deliver on the gsignal stack, where the Linux kernel would deliver (minitSignalStack installs gsignal as the alt stack on every M).
	ntSignalTramp(handler, uintptr(sig), unsafe.Pointer(&info), unsafe.Pointer(&uc), gp.m.gsignal.stack.hi)
}

// ntWinthrowLine writes one line about the exception with ntwrite1
// alone: code, PC, the access kind and address, SP, the g the thread
// carries, and whether a panic was already under way.
//
//go:nosplit
func ntWinthrowLine(info *ntExceptionRecord, r *ntContext, gp *g, nested bool) {
	var line [200]byte
	n := copy(line[:], "runtime: NT exception 0x")
	n = ntHexInto(line[:], n, uintptr(info.exceptionCode))
	n += copy(line[n:], " pc=0x")
	n = ntHexInto(line[:], n, r.getPC())
	n += copy(line[n:], " kind=")
	n = ntHexInto(line[:], n, info.exceptionInformation[0])
	n += copy(line[n:], " addr=0x")
	n = ntHexInto(line[:], n, info.exceptionInformation[1])
	n += copy(line[n:], " sp=0x")
	n = ntHexInto(line[:], n, r.getSP())
	n += copy(line[n:], " g=0x")
	n = ntHexInto(line[:], n, uintptr(unsafe.Pointer(gp)))
	n += copy(line[n:], " tebg=0x")
	n = ntHexInto(line[:], n, uintptr(unsafe.Pointer(getg())))
	if nested {
		n += copy(line[n:], " while panicking")
	}
	line[n] = 0x0a
	n++
	ntwrite1(2, unsafe.Pointer(&line[0]), int32(n))
}

// ntHexInto writes v in hex at line[n:] and returns the new length.
//
//go:nosplit
func ntHexInto(line []byte, n int, v uintptr) int {
	started := false
	for shift := 60; shift >= 0; shift -= 4 {
		d := byte(v>>uint(shift)) & 0xf
		if d == 0 && !started && shift != 0 {
			continue
		}
		started = true
		if d < 10 {
			line[n] = '0' + d
		} else {
			line[n] = 'a' + d - 10
		}
		n++
	}
	return n
}
