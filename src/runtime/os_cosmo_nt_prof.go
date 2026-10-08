// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// Windows NT CPU profiling: upstream os_windows.go's
// profileLoop/profilem ported to the NT function-table idiom.
//
// There is no SIGPROF on NT. The model, upstream's, is a dedicated
// profiler M parked on a waitable timer: each tick it walks allm,
// suspends each eligible M's thread, reads. Its context and calls
// sigprof DIRECTLY - the same host-independent recording routine the
// unix SIGPROF handler calls, reached without any signal. So the
// setitimer asm and the ENOSYS SYS_SETITIMER dispatch stay unreachable.
//
// The profiler runs on a REAL M (newm, no P), so it has g0 and TLS and
// may use the plain ntcall trampolines. It never entersyscalls.
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

// ntProfTimer is the process-wide profiling timer ntProfileLoop parks on.
var ntProfTimer uintptr

// ntSetProcessCPUProfiler is setProcessCPUProfiler's NT leg: on first use
// create the waitable timer and start the profiler M. Upstream
// setProcessCPUProfiler's shape; the profiletimer==0 check is the once-guard,
// sound because every call runs under prof.signalLock (setcpuprofilerate).
func ntSetProcessCPUProfiler(hz int32) {
	if ntProfTimer != 0 {
		return
	}
	// Highest-resolution timer the host offers, degrading gracefully.
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
		// Every fallback failed, so no sample will ever be taken.
		throw("cosmo: NT CPU profiling: no waitable timer")
	}
	atomic.Storeuintptr(&ntProfTimer, timer)
	newm(ntProfileLoop, nil, -1)
}

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

// ntProfileLoop runs on the standing profiler M (newm, no P): upstream
// profileLoop. Each timer tick walks allm and samples every M that is minit'd
// (mp.thread != 0), wants profiling (mp.profilehz != 0), and is not parked on
// a note (mp.blocked - maintained by lock_futex.go on cosmo). Ms parked
// inside WSAPoll (the netpoller) are NOT note-blocked, so they are sampled
// and attribute to external code - upstream windows behavior exactly.
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

			// mp may exit between the DuplicateHandle above and the SuspendThread.
			lock(&ntSuspendLock)
			if int32(uint32(ntcall(ntSuspendThreadFn, thread, 0, 0, 0, 0, 0))) == -1 {
				// The thread no longer exists.
				unlock(&ntSuspendLock)
				ntcall(ntCloseHandleFn, thread, 0, 0, 0, 0, 0)
				continue
			}
			// Re-check under suspension: mp may be shutting down.
			if mp.profilehz != 0 && !mp.blocked {
				ntProfileM(mp, thread)
			}
			ntcall(ntResumeThreadFn, thread, 0, 0, 0, 0, 0)
			unlock(&ntSuspendLock)
			ntcall(ntCloseHandleFn, thread, 0, 0, 0, 0, 0)
		}
	}
}

// ntProfileM records one CPU sample of mp, whose thread (handle thread) is
// suspended: upstream profilem. CONTEXT_CONTROL suffices - sigprof consumes
// only pc, sp and lr - and unlike preemption nothing is written back, so the
// thread's other registers are never touched.
func ntProfileM(mp *m, thread uintptr) {
	// 16-align the CONTEXT buffer (the ntPreemptM idiom).
	var c *ntContext
	var cbuf [unsafe.Sizeof(*c) + 15]byte
	c = (*ntContext)(unsafe.Pointer((uintptr(unsafe.Pointer(&cbuf[15]))) &^ 15))
	c.contextFlags = _NT_CONTEXT_CONTROL

	if ntcall(ntGetThreadContextFn, thread, uintptr(unsafe.Pointer(c)), 0, 0, 0, 0) == 0 {
		// Cannot read the context; skip the sample. (No print: a thread is suspended.)
		return
	}
	gp := ntGFromSP(mp, c.getSP())
	sigprof(c.getPC(), c.getSP(), c.getLR(), gp, mp)
}
