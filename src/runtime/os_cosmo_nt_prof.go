// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

// Windows NT CPU profiling (wave 3 item 3): upstream os_windows.go's
// profileLoop/profilem ported to the NT function-table idiom.
//
// There is no SIGPROF on NT. Upstream Go's model - which this file
// copies - is a dedicated profiler M parked on a waitable timer: each
// tick it walks allm, suspends each eligible M's thread, reads its
// context, and calls sigprof(pc, sp, lr, gp, mp) DIRECTLY - the same
// host-independent sample-recording routine the unix SIGPROF handler
// calls, reached without any signal. No Linux signal number is ever
// involved, so "SIGPROF parity" needs no numbering translation at
// all; the setitimer asm and the SYS_SETITIMER=38 ENOSYS dispatch
// stay untouched and unreachable on NT.
//
// The profiler runs on a REAL M (newm, no P) - the ntCtrlRelay
// precedent - so it has g0 and TLS and may use the plain ntcall
// trampolines; the foreign-thread prohibition (console-handler rule)
// never applies. It never entersyscalls (no P to hand back), so all
// waits are plain ntcall, never ntcallSE.
//
// ONE deliberate divergence from upstream: upstream's profileLoop
// suspends threads without any suspension lock. Here every
// SuspendThread in the runtime happens under ntSuspendLock (the
// wave-2 contract): ntExit takes that lock FOREVER before
// ExitProcess, so a profile tick can never be mid-suspension while
// the process dies (the suspender-killed-mid-suspend wedge upstream
// exit() only guards against for preemption). Profiling ticks at
// 10ms; serializing them against preemption's suspensions costs
// nothing measurable.
//
// Deadlock discipline (audited; recorded in DEBUGGING.md wave-3
// item 3):
//   - Lock order matches ntPreemptM exactly: mp.threadLock is taken
//     and RELEASED before ntSuspendLock; nothing is ever suspended
//     while this M holds any threadLock, and no threadLock is taken
//     under ntSuspendLock.
//   - A suspended thread may hold arbitrary locks, so between
//     SuspendThread and ResumeThread this M runs only suspension-safe
//     code: sigprof and its callees are the exact set the unix
//     SIGPROF handler runs with a thread interrupted at an arbitrary
//     point (cpuprof.add: "called from signal handlers ... cannot
//     allocate memory or acquire locks that might be held at the time
//     of the signal"). The one lock down there, prof.signalLock, is a
//     CAS spinlock whose only other taker is setcpuprofilerate -
//     which clears THIS thread's mp.profilehz before acquiring it, so
//     the post-suspend profilehz re-check below guarantees the
//     suspended thread does not hold it. No print/throw/alloc happens
//     while any thread is suspended (sigprof's mallocing++ trap
//     enforces the alloc part).
package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

const (
	_NT_CREATE_WAITABLE_TIMER_HIGH_RESOLUTION = 0x2
	_NT_TIMER_ALL_ACCESS                      = 0x1F0003
	_NT_THREAD_PRIORITY_HIGHEST               = 2
)

// ntProfTimer is the process-wide profiling timer ntProfileLoop parks
// on. Created once by ntSetProcessCPUProfiler (which runs under
// prof.signalLock, serializing the create), armed and disarmed by
// ntSetThreadCPUProfiler. 0 until profiling first starts - and 0
// forever if every timer-creation fallback failed, in which case
// profiling stays silently sampleless (the pre-item-3 behavior)
// rather than hot-looping on an invalid handle.
var ntProfTimer uintptr

// ntSetProcessCPUProfiler is setProcessCPUProfiler's NT leg: on first
// use create the waitable timer and start the profiler M. Upstream
// setProcessCPUProfiler's shape; the profiletimer==0 check is the
// once-guard, sound because every call runs under prof.signalLock
// (setcpuprofilerate).
func ntSetProcessCPUProfiler(hz int32) {
	if ntProfTimer != 0 {
		return
	}
	// Highest-resolution timer the host offers, degrading gracefully:
	// HIGH_RESOLUTION CreateWaitableTimerExW (Win10 1803+) -> plain
	// CreateWaitableTimerExW -> CreateWaitableTimerW. Without
	// HIGH_RESOLUTION the default timer coalesces to the 15.6ms
	// quantum (~64Hz effective against the 10ms period) - accepted;
	// the sample-count assertions this feeds are >=1, not rate-based.
	var timer uintptr
	if ntCreateWaitableTimerExWFn != 0 {
		timer = ntcall(ntCreateWaitableTimerExWFn, 0, 0,
			_NT_CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, _NT_TIMER_ALL_ACCESS, 0, 0)
		if timer == 0 {
			timer = ntcall(ntCreateWaitableTimerExWFn, 0, 0, 0, _NT_TIMER_ALL_ACCESS, 0, 0)
		}
	}
	if timer == 0 {
		timer = ntcall(ntCreateWaitableTimerWFn, 0, 0, 0, 0, 0, 0) // auto-reset, unnamed
	}
	if timer == 0 {
		return
	}
	atomic.Storeuintptr(&ntProfTimer, timer)
	newm(ntProfileLoop, nil, -1)
}

// ntSetThreadCPUProfiler arms (hz > 0) or disarms (hz <= 0) the
// profiling timer: upstream setThreadCPUProfiler's exact due/period
// math. due is negative (relative time, 100ns units); the disarm
// shape is a one-shot ~29,000 years out. The caller
// (setThreadCPUProfiler, os_cosmo.go) stores mp.profilehz afterwards
// for every host.
func ntSetThreadCPUProfiler(hz int32) {
	if ntProfTimer == 0 {
		return
	}
	ms := int32(0)
	due := ^int64(^uint64(1 << 63))
	if hz > 0 {
		ms = 1000 / hz
		if ms == 0 {
			ms = 1
		}
		due = int64(ms) * -10000
	}
	ntcall(ntSetWaitableTimerFn, ntProfTimer,
		uintptr(unsafe.Pointer(&due)), uintptr(ms), 0, 0, 0)
}

// ntProfileLoop runs on the standing profiler M (newm, no P):
// upstream profileLoop. Each timer tick walks allm and samples every
// M that is minit'd (mp.thread != 0), wants profiling
// (mp.profilehz != 0), and is not parked on a note (mp.blocked -
// maintained by lock_futex.go on cosmo). Ms parked inside WSAPoll
// (the netpoller) are NOT note-blocked, so they are sampled and
// attribute to external code - upstream windows behavior exactly.
func ntProfileLoop() {
	if ntSetThreadPriorityFn != 0 {
		// Best-effort: sample even when every P is busy spinning.
		ntcall(ntSetThreadPriorityFn, _NT_CURRENT_THREAD, _NT_THREAD_PRIORITY_HIGHEST, 0, 0, 0, 0)
	}
	for {
		ntcall(ntWaitForSingleObjectFn, ntProfTimer, _NT_INFINITE, 0, 0, 0, 0)
		first := (*m)(atomic.Loadp(unsafe.Pointer(&allm)))
		for mp := first; mp != nil; mp = mp.alllink {
			if mp == getg().m {
				// Don't profile ourselves.
				continue
			}

			lock(&mp.threadLock)
			// Do not profile threads blocked on Notes (idle worker
			// threads, idle timer thread, idle heap scavenger, ...).
			if mp.thread == 0 || mp.profilehz == 0 || mp.blocked {
				unlock(&mp.threadLock)
				continue
			}
			// Acquire our own handle to the thread.
			var thread uintptr
			if ntcall7(ntDuplicateHandleFn,
				_NT_CURRENT_PROCESS, mp.thread, _NT_CURRENT_PROCESS,
				uintptr(unsafe.Pointer(&thread)),
				0, 0, _NT_DUPLICATE_SAME_ACCESS) == 0 {
				print("runtime: profileLoop duplicatehandle failed; errno=", getg().m.ntLastError, "\n")
				throw("profileLoop: duplicatehandle failed")
			}
			unlock(&mp.threadLock)

			// mp may exit between the DuplicateHandle above and the
			// SuspendThread. The handle stays valid, but SuspendThread
			// then fails. Serialize the suspension under ntSuspendLock
			// (divergence from upstream's lock-free walk; see the file
			// comment) and hold it until ResumeThread: GetThreadContext
			// is what completes the suspension, and ntExit must never
			// interleave with any of this window.
			lock(&ntSuspendLock)
			if int32(uint32(ntcall(ntSuspendThreadFn, thread, 0, 0, 0, 0, 0))) == -1 {
				// The thread no longer exists.
				unlock(&ntSuspendLock)
				ntcall(ntCloseHandleFn, thread, 0, 0, 0, 0, 0)
				continue
			}
			// Re-check under suspension: mp may be shutting down, and
			// setcpuprofilerate clears its caller's profilehz BEFORE
			// taking prof.signalLock - this re-read is what guarantees
			// a thread suspended inside that critical section is never
			// sampled (see the file comment's deadlock discipline).
			if mp.profilehz != 0 && !mp.blocked {
				ntProfileM(mp, thread)
			}
			ntcall(ntResumeThreadFn, thread, 0, 0, 0, 0, 0)
			unlock(&ntSuspendLock)
			ntcall(ntCloseHandleFn, thread, 0, 0, 0, 0, 0)
		}
	}
}

// ntProfileM records one CPU sample of mp, whose thread (handle
// thread) is suspended: upstream profilem. CONTEXT_CONTROL suffices -
// sigprof consumes only pc, sp and lr - and unlike preemption nothing
// is written back, so the thread's other registers are never touched.
func ntProfileM(mp *m, thread uintptr) {
	// 16-align the CONTEXT buffer (the ntPreemptM idiom).
	var c *ntContext
	var cbuf [unsafe.Sizeof(*c) + 15]byte
	c = (*ntContext)(unsafe.Pointer((uintptr(unsafe.Pointer(&cbuf[15]))) &^ 15))
	c.contextFlags = _NT_CONTEXT_CONTROL

	if ntcall(ntGetThreadContextFn, thread, uintptr(unsafe.Pointer(c)), 0, 0, 0, 0) == 0 {
		// Cannot read the context; skip the sample. (No print: a
		// thread is suspended.)
		return
	}
	gp := ntGFromSP(mp, c.getSP())
	sigprof(c.getPC(), c.getSP(), c.getLR(), gp, mp)
}
