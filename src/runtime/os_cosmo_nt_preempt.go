// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

// Windows NT async preemption and console control (wave 2 chunk D2).
//
// Async preemption is upstream os_windows.go's preemptM ported to the
// NT function-table idiom: suspend the target M's thread
// (SuspendThread), wait for the suspension to complete
// (GetThreadContext - SuspendThread alone only queues it), and if the
// interrupted PC/SP is an async-safe point of a goroutine that wants
// preemption, rewrite the saved CONTEXT so the thread calls
// asyncPreempt on resume (SP-=8, [SP]=resume PC, RIP=asyncPreempt -
// the same fake-CALL shape the VEH uses for sigpanic0). Three locks
// keep this sound, all with upstream's exact semantics:
//
//   - mp.preemptExtLock (CAS): ntPreemptM vs external code on the
//     target thread. Foreign calls that can block or take the loader
//     lock (the ntcallSE/ntcallSE10 bracket) hold it, so a preemption
//     attempt against a thread in win64 code fails fast instead of
//     suspending a thread that might be mid-ExitProcess.
//   - mp.threadLock: guards mp.thread (the per-M duplicated thread
//     handle) between ntPreemptM's DuplicateHandle and
//     minit/unminit's install/close.
//   - ntSuspendLock: serializes ALL SuspendThread callers -
//     SuspendThread is asynchronous, so two threads suspending each
//     other would deadlock - held until GetThreadContext confirms the
//     suspension. ntExit takes it FOREVER before ExitProcess, so no
//     suspension can be mid-flight while the process dies (the
//     suspender-killed-mid-suspend wedge, upstream exit()).
//
// The scheduler reaches this machinery through the fork's shared
// signalM (os_cosmo.go): preemptM (signal_unix.go) CASes
// mp.signalPending and calls signalM(mp, sigPreempt), whose NT leg
// calls ntPreemptM - which therefore also clears signalPending after
// acking preemptGen (the unix ack protocol doSigPreempt performs on
// signal-delivery hosts; without the clear the wrapper's CAS gate
// would stay shut and preemption would fire exactly once).
//
// Console control: SetConsoleCtrlHandler's callback runs on a thread
// INJECTED by Windows - no g, no TLS, no Go. The asm handler
// (ntCtrlTramp, sys_cosmo_nt_<goarch>.s) only sets a bit in ntCtrlMask,
// SetEvents ntCtrlEvent, and returns 1 (CTRL_C -> SIGINT, CTRL_BREAK
// -> SIGQUIT) or blocks forever (CLOSE -> SIGHUP, LOGOFF/SHUTDOWN ->
// SIGTERM - Windows kills the process the moment such a handler
// returns; blocking gives Go handlers the OS's grace window to clean
// up, upstream ctrlHandler's block()). The BREAK -> SIGQUIT and
// CLOSE -> SIGHUP legs deliberately diverge from upstream Go's
// windows mapping (BREAK -> SIGINT, CLOSE -> SIGTERM) for unix
// parity: Ctrl-Break is the SIGQUIT chord - on a wedged process an
// unwatched SIGQUIT produces the goroutine dump - and closing the
// console window is a hangup (DEBUGGING.md wave 3 item 4). A
// dedicated relay M - created via newm at boot, parked in
// WaitForSingleObject on its g0 - picks the event up and feeds
// ntKillSelf(sig), which consults ntSigActs and either drops
// (SIG_IGN), dies with the encoded status (SIG_DFL - matching the
// Linux default action for SIGINT/SIGHUP/SIGTERM), or delivers
// through the D1 trampoline into sigtrampgo -> sighandler ->
// sigsend/os signal (unwatched SIGQUIT: goroutine dump + encoded
// death, the fork sigtable's _SigThrow - Linux parity).

package runtime

import (
	"internal/abi"
	"internal/runtime/atomic"
	"unsafe"
)

// _NT_CURRENT_THREAD is the GetCurrentThread() pseudo-handle (-2).
// The CONTEXT flag word that goes with it is architecture-dependent:
// _NT_CONTEXT_CONTROL, in os_cosmo_nt_ctx_<goarch>.go.
const _NT_CURRENT_THREAD = ^uintptr(1)

// ntSuspendLock protects simultaneous SuspendThread operations from
// suspending each other (see the file comment).
var ntSuspendLock mutex

// ntExiting is set when the process is exiting (under ntSuspendLock).
var ntExiting uint32

// ntDeadlock freezes a thread that lost the CreateThread-vs-
// ExitProcess race (ntNewosproc): lock it twice and never return.
var ntDeadlock mutex

// ntMinitThread is minit's NT leg: duplicate this thread's
// pseudo-handle into a real one for ntPreemptM. Runs on the new
// thread, before it schedules anything - so the M is preemptible from
// the moment it can run user code. Cannot allocate.
func ntMinitThread() {
	var thandle uintptr
	if ntcall7(ntDuplicateHandleFn,
		_NT_CURRENT_PROCESS, _NT_CURRENT_THREAD, _NT_CURRENT_PROCESS,
		uintptr(unsafe.Pointer(&thandle)),
		0, 0, _NT_DUPLICATE_SAME_ACCESS) == 0 {
		print("runtime.minit: duplicatehandle failed; errno=", getg().m.ntLastError, "\n")
		throw("runtime.minit: duplicatehandle failed")
	}
	mp := getg().m
	lock(&mp.threadLock)
	mp.thread = thandle
	unlock(&mp.threadLock)
}

// ntUnminitThread is unminit's NT leg: close the duplicated handle so
// ntPreemptM treats this M as unpreemptible (mp.thread == 0) before
// the thread can exit.
//
//go:nosplit
func ntUnminitThread() {
	mp := getg().m
	lock(&mp.threadLock)
	if mp.thread != 0 {
		ntcall(ntCloseHandleFn, mp.thread, 0, 0, 0, 0, 0)
		mp.thread = 0
	}
	unlock(&mp.threadLock)
}

// ntGFromSP returns the g that mp's thread is executing on, judged by
// the interrupted stack pointer: one of g0, gsignal, or curg
// (upstream os_windows.go gFromSP).
func ntGFromSP(mp *m, sp uintptr) *g {
	if gp := mp.g0; gp != nil && gp.stack.lo < sp && sp < gp.stack.hi {
		return gp
	}
	if gp := mp.gsignal; gp != nil && gp.stack.lo < sp && sp < gp.stack.hi {
		return gp
	}
	if gp := mp.curg; gp != nil && gp.stack.lo < sp && sp < gp.stack.hi {
		return gp
	}
	return nil
}

// ntPreemptAck acknowledges a preemption attempt: bump preemptGen
// (the requesting P spins on it) and reopen preemptM's signalPending
// CAS gate - the unix ack order doSigPreempt uses.
func ntPreemptAck(mp *m) {
	mp.preemptGen.Add(1)
	mp.signalPending.Store(0)
}

// ntPreemptM sends an async-preemption request to mp: upstream
// os_windows.go preemptM, on the NT function table. Every path acks
// (ntPreemptAck) so the requester never spins forever.
func ntPreemptM(mp *m) {
	if mp == getg().m {
		throw("self-preempt")
	}

	// Synchronize with external code that may try to ExitProcess.
	if !atomic.Cas(&mp.preemptExtLock, 0, 1) {
		// External code is running. Fail the preemption attempt.
		ntPreemptAck(mp)
		return
	}

	// Acquire our own handle to mp's thread.
	lock(&mp.threadLock)
	if mp.thread == 0 {
		// The M hasn't been minit'd yet (or was just unminit'd).
		unlock(&mp.threadLock)
		atomic.Store(&mp.preemptExtLock, 0)
		ntPreemptAck(mp)
		return
	}
	var thread uintptr
	if ntcall7(ntDuplicateHandleFn,
		_NT_CURRENT_PROCESS, mp.thread, _NT_CURRENT_PROCESS,
		uintptr(unsafe.Pointer(&thread)),
		0, 0, _NT_DUPLICATE_SAME_ACCESS) == 0 {
		print("runtime.preemptM: duplicatehandle failed; errno=", getg().m.ntLastError, "\n")
		throw("runtime.preemptM: duplicatehandle failed")
	}
	unlock(&mp.threadLock)

	// Prepare the thread context buffer. This must be aligned to 16
	// bytes.
	var c *ntContext
	var cbuf [unsafe.Sizeof(*c) + 15]byte
	c = (*ntContext)(unsafe.Pointer((uintptr(unsafe.Pointer(&cbuf[15]))) &^ 15))
	c.contextFlags = _NT_CONTEXT_CONTROL

	// Serialize thread suspension. SuspendThread is asynchronous, so
	// it's otherwise possible for two threads to suspend each other
	// and deadlock. We must hold this lock until after
	// GetThreadContext, since that blocks until the thread is
	// actually suspended.
	lock(&ntSuspendLock)

	// Suspend the thread.
	if int32(uint32(ntcall(ntSuspendThreadFn, thread, 0, 0, 0, 0, 0))) == -1 {
		unlock(&ntSuspendLock)
		ntcall(ntCloseHandleFn, thread, 0, 0, 0, 0, 0)
		atomic.Store(&mp.preemptExtLock, 0)
		// The thread no longer exists. This shouldn't be possible,
		// but just acknowledge the request.
		ntPreemptAck(mp)
		return
	}

	// We have to be very careful between this point and once we've
	// shown mp is at an async safe-point. Like a signal handler, mp
	// could have been doing anything when we stopped it, including
	// holding arbitrary locks.

	// We have to get the thread context before inspecting the M
	// because SuspendThread only requests a suspend.
	// GetThreadContext actually blocks until it's suspended.
	ntcall(ntGetThreadContextFn, thread, uintptr(unsafe.Pointer(c)), 0, 0, 0, 0)

	unlock(&ntSuspendLock)

	// Does it want a preemption and is it safe to preempt?
	gp := ntGFromSP(mp, c.getSP())
	if gp != nil && wantAsyncPreempt(gp) {
		if ok, resumePC := isAsyncSafePoint(gp, c.getPC(), c.getSP(), c.getLR()); ok {
			// Inject a call to asyncPreempt: the fake-CALL
			// arrangement upstream PushCall performs. The stack write
			// inside pushCall is a plain store from this thread; the
			// target is suspended and goroutine stacks are ordinary
			// memory.
			c.pushCall(abi.FuncPCABI0(asyncPreempt), resumePC)
			ntcall(ntSetThreadContextFn, thread, uintptr(unsafe.Pointer(c)), 0, 0, 0, 0)
		}
	}

	atomic.Store(&mp.preemptExtLock, 0)

	// Acknowledge the preemption.
	ntPreemptAck(mp)

	ntcall(ntResumeThreadFn, thread, 0, 0, 0, 0, 0)
	ntcall(ntCloseHandleFn, thread, 0, 0, 0, 0, 0)
}

// ntExit is the NT leg of runtime.exit (tail-jumped from the amd64
// exit asm). Disallow thread suspension for preemption
// before dying: otherwise ExitProcess and SuspendThread can race -
// SuspendThread queues a suspension request for this thread,
// ExitProcess kills the suspending thread, and then this thread
// suspends, wedging the exit (upstream os_windows.go exit()).
//
//go:nosplit
func ntExit(code int32) {
	lock(&ntSuspendLock)
	atomic.Store(&ntExiting, 1)
	ntcall(ntExitProcessFn, uintptr(uint32(code)), 0, 0, 0, 0, 0)
	ntCrash(0xfe) // unreachable
}

// ntExitEncodedOrdered is ntExitEncoded behind the same suspension
// discipline as ntExit, for encoded signal deaths reached from
// ordinary Go context (ntKillSelf's SIGKILL/default-terminate
// decisions). Crash paths (ntWinthrow, raise/dieFromSignal) keep
// calling the bare asm ntExitEncoded - taking runtime locks from an
// exception handler or a dying context is worse than upstream's
// accepted dieFromException race.
func ntExitEncodedOrdered(sig uint32) {
	lock(&ntSuspendLock)
	atomic.Store(&ntExiting, 1)
	ntExitEncoded(sig)
}

// ---- console control ----

// ntCtrlEvent is the auto-reset event the asm console-ctrl handler
// (ntCtrlTramp) signals; ntCtrlRelay waits on it. Created before the
// handler is registered, never closed.
var ntCtrlEvent uintptr

// ntCtrlMask holds pending console-ctrl signals as bits (1<<_SIGHUP |
// 1<<_SIGINT | 1<<_SIGQUIT | 1<<_SIGTERM). The asm handler ORs bits
// in (LOCK ORL, from a foreign thread); the relay drains with Xchg.
var ntCtrlMask uint32

// ntCtrlTramp is the SetConsoleCtrlHandler callback (asm,
// sys_cosmo_nt_<goarch>.s): Go-free, since it runs on an injected
// foreign thread.
func ntCtrlTramp()

// ntInitConsoleCtrl wires console-control events into os/signal:
// create the relay event, park a relay M on it, and register the asm
// handler. Called from goenvs's NT branch (upstream registers its
// ctrl handler in goenvs too) - after mallocinit, so newm is fine,
// and before user code runs. Degrades to no console-ctrl support if
// the event cannot be created.
func ntInitConsoleCtrl() {
	ev := ntcall(ntCreateEventWFn, 0, 0, 0, 0, 0, 0) // auto-reset, nonsignaled, unnamed
	if ev == 0 {
		return
	}
	ntCtrlEvent = ev
	newm(ntCtrlRelay, nil, -1)
	ntcall(ntSetConsoleCtrlHandlerFn, abi.FuncPCABI0(ntCtrlTramp), 1, 0, 0, 0, 0)
}

// ntCtrlRelay runs on its own M (no P), parked in WaitForSingleObject
// on the relay event. Each wakeup drains the pending mask and runs
// the kernel delivery decision for each signal on THIS thread -
// ntKillSelf consults ntSigActs exactly like a kill(2): SIG_IGN
// drops, SIG_DFL dies with the encoded status (the Linux default
// action for SIGHUP/SIGINT/SIGTERM), an installed handler delivers
// through the D1 trampoline into sighandler (sigsend to os/signal for
// watched signals, dieFromSignal for unwatched fatal ones - unwatched
// SIGQUIT additionally dumps goroutines, _SigThrow). Drain order:
// the keyboard chords first (SIGINT, SIGQUIT), then the lifetime
// events (SIGHUP, SIGTERM), so a coalesced wakeup dies for the
// blocked-handler event only after the interactive chords ran.
func ntCtrlRelay() {
	for {
		ntcall(ntWaitForSingleObjectFn, ntCtrlEvent, _NT_INFINITE, 0, 0, 0, 0)
		for {
			mask := atomic.Xchg(&ntCtrlMask, 0)
			if mask == 0 {
				break
			}
			if mask&(1<<_SIGINT) != 0 {
				ntKillSelf(_SIGINT)
			}
			if mask&(1<<_SIGQUIT) != 0 {
				ntKillSelf(_SIGQUIT)
			}
			if mask&(1<<_SIGHUP) != 0 {
				ntKillSelf(_SIGHUP)
			}
			if mask&(1<<_SIGTERM) != 0 {
				ntKillSelf(_SIGTERM)
			}
		}
	}
}
