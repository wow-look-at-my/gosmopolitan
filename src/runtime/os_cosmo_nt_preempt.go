// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// Windows NT async preemption and console control.
//
// The scheduler reaches this through the fork's shared signalM
// (os_cosmo.go): preemptM CASes mp.signalPending and calls
// signalM(mp, sigPreempt), whose NT leg is ntPreemptM.

package runtime

import (
	"internal/abi"
	"internal/runtime/atomic"
	"unsafe"
)

const _NT_CURRENT_THREAD = ^uintptr(1)

// ntSuspendLock serializes ALL SuspendThread callers: SuspendThread is asynchronous.
var ntSuspendLock mutex

// ntExiting is set when the process is exiting (under ntSuspendLock).
var ntExiting uint32

// ntDeadlock freezes a thread that lost the CreateThread-vs- ExitProcess race (ntNewosproc).
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

// ntPreemptExtRelease drops the mp.preemptExtLock ntPreemptM took and wakes
// mp's thread if osPreemptExtEnter is waiting for it.
func ntPreemptExtRelease(mp *m) {
	atomic.Store(&mp.preemptExtLock, 0)
	ntFutexwakeup(&mp.preemptExtLock)
}

// ntPreemptAck acknowledges a preemption attempt.
func ntPreemptAck(mp *m) {
	mp.preemptGen.Add(1)
	mp.signalPending.Store(0)
}

// ntPreemptM sends an async-preemption request to mp: upstream os_windows.go
// preemptM, on the NT function table. It suspends the target thread, waits
// for the suspension with GetThreadContext - SuspendThread alone only queues
// it - and. That thread is at an async-safe point, rewrites the saved CONTEXT
// calls asyncPreempt on resume. Every path acks, so the requester never spins
// forever.
//
// Locks keep this sound, with upstream's exact semantics. mp.preemptExtLock
// fails a preemption fast against a thread in win64 code, which might be
// mid-ExitProcess. mp.threadLock guards mp.thread between the DuplicateHandle
// here and minit/unminit. ntSuspendLock is documented on its own declaration.
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
		// The M hasn't been minit'd yet (or was unminit'd).
		unlock(&mp.threadLock)
		ntPreemptExtRelease(mp)
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

	// Prepare the thread context buffer. This must be aligned to several bytes.
	var c *ntContext
	var cbuf [unsafe.Sizeof(*c) + 15]byte
	c = (*ntContext)(unsafe.Pointer((uintptr(unsafe.Pointer(&cbuf[15]))) &^ 15))
	c.contextFlags = _NT_CONTEXT_CONTROL

	// Serialize thread suspension.
	lock(&ntSuspendLock)

	// Suspend the thread.
	if int32(uint32(ntcall(ntSuspendThreadFn, thread, 0, 0, 0, 0, 0))) == -1 {
		unlock(&ntSuspendLock)
		ntcall(ntCloseHandleFn, thread, 0, 0, 0, 0, 0)
		ntPreemptExtRelease(mp)
		// The thread no longer exists. This shouldn't be possible, but acknowledge the request.
		ntPreemptAck(mp)
		return
	}

	// We have to be careful between this point and once we've shown mp is at an async safe-point.

	// We have to get the thread context before inspecting the M because SuspendThread only requests a suspend.
	ntcall(ntGetThreadContextFn, thread, uintptr(unsafe.Pointer(c)), 0, 0, 0, 0)

	unlock(&ntSuspendLock)

	// Does it want a preemption and is it safe to preempt?
	gp := ntGFromSP(mp, c.getSP())
	if gp != nil && wantAsyncPreempt(gp) {
		if ok, resumePC := isAsyncSafePoint(gp, c.getPC(), c.getSP(), c.getLR()); ok {
			// Inject a call to asyncPreempt: the fake-CALL arrangement upstream PushCall performs.
			c.pushCall(abi.FuncPCABI0(asyncPreempt), resumePC)
			ntcall(ntSetThreadContextFn, thread, uintptr(unsafe.Pointer(c)), 0, 0, 0, 0)
		}
	}

	atomic.Store(&mp.preemptExtLock, 0)

	// Acknowledge the preemption.
	ntPreemptAck(mp)

	ntcall(ntResumeThreadFn, thread, 0, 0, 0, 0, 0)
	ntcall(ntCloseHandleFn, thread, 0, 0, 0, 0, 0)

	// The thread runs again, so a wait in osPreemptExtEnter can end.
	ntFutexwakeup(&mp.preemptExtLock)
}

// ntExit is the NT leg of runtime.exit (tail-jumped from the amd64 exit asm).
//
//go:nosplit
func ntExit(code int32) {
	ntBootCode("ntExit", uintptr(uint32(code)))
	lock(&ntSuspendLock)
	atomic.Store(&ntExiting, 1)
	ntcall(ntExitProcessFn, uintptr(uint32(code)), 0, 0, 0, 0, 0)
	ntCrash(0xfe) // unreachable
}

// ntExitEncodedOrdered is ntExitEncoded behind the same suspension discipline
// as ntExit, for encoded signal deaths reached.
func ntExitEncodedOrdered(sig uint32) {
	lock(&ntSuspendLock)
	atomic.Store(&ntExiting, 1)
	ntExitEncoded(sig)
}

// ---- console control ----

// ntCtrlEvent is the auto-reset event the asm console-ctrl handler (ntCtrlTramp) signals; ntCtrlRelay waits on it.
var ntCtrlEvent uintptr

var ntCtrlMask uint32

// ntCtrlTramp is the SetConsoleCtrlHandler callback (asm, sys_cosmo_nt_<goarch>.s): Go-free.
func ntCtrlTramp()

// ntInitConsoleCtrl wires console-control events into os/signal: create the
// relay event, park a relay M on it, and register the asm handler.
func ntInitConsoleCtrl() {
	ev := ntcall(ntCreateEventWFn, 0, 0, 0, 0, 0, 0) // auto-reset, nonsignaled, unnamed
	if ev == 0 {
		return
	}
	ntCtrlEvent = ev
	newm(ntCtrlRelay, nil, -1)
	ntcall(ntSetConsoleCtrlHandlerFn, abi.FuncPCABI0(ntCtrlTramp), 1, 0, 0, 0, 0)
}

// ntCtrlRelay runs on its own M (no P), parked in WaitForSingleObject on the
// relay event.
func ntCtrlRelay() {
	// A system M, like sysmon and the template thread: checkdead must not count it as a thread that can run goroutines.
	lock(&sched.lock)
	sched.nmsys++
	checkdead()
	unlock(&sched.lock)

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
