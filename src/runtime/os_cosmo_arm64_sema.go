// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

// Semaphore implementation for cosmo arm64, used by lock_spinbit.go and
// lock_sema.go for M parking. The host OS is only known at run time: on
// Linux the M's semaphore is a counting semaphore built on the futex
// syscall; on XNU it is upstream os_darwin.go's design ported verbatim -
// a per-M pthread_mutex/pthread_cond pair (resolved from Apple libc via
// dlsym) guarding a count.
//
// XNU parking used to be Syslib dispatch semaphores. That primitive
// nondeterministically LOST wakeups under load: the wave-6..8 macOS CI
// wedge, where an M slept through the dispatch_semaphore_signal for a
// runtime lock that was provably released every 100ms (DEBUGGING.md
// 2026-07-02, wave 8, has the full forensic chain: poll-level loss,
// wake-decision logic and wait-side-stuck theories were each eliminated
// on CI evidence, leaving the semaphore's signal side / M identity).
// Upstream darwin pointedly parks Ms on pthread primitives, not
// dispatch semaphores; this file is that design.
//
// The pthread calls go through asmcgocall (via the trampolines in
// sys_cosmo_arm64.s), which switches to the g0 stack first: unlike the
// shallow sysret wrappers the rest of the darwin emulation resolves,
// pthread_mutex_lock and pthread_cond_wait are real C functions with
// real frames, and semasleep/semawakeup run on arbitrary stacks
// (contended lock2 parks from user g stacks, e.g. channel operations).
// Upstream matches: its libcCall wraps asmcgocall. Like upstream's
// libcCall, cosmoPthreadLibcCall below records m.libcallg/pc/sp
// around the call, so a SIGPROF landing inside pthread_cond_wait/
// mutex attributes to the Go call site (usesLibcall lists cosmo;
// sigprof's libcall unwind branch consumes the fields - wired
// together with the darwin setitimer bring-up, as the wave-9 charter
// required).
//
// Fork-child note (matches upstream darwin): a forked child's pthread
// state is undefined, but the child path between fork and execve
// (exec_cosmo_arm64.go) is nosplit, lock-free and calls only
// pre-resolved async-signal-safe functions, so it can never reach
// semasleep/semawakeup. Nothing to reinitialize.

import (
	"internal/abi"
	"internal/runtime/atomic"
	"internal/runtime/sys"
	"unsafe"
)

// _ETIMEDOUT_xnu is Apple's ETIMEDOUT. The pthread_* functions here are
// raw libc functions resolved via dlsym: their return values are Apple
// errno numbers, not sysret-wrapped and not translated to Linux
// numbering.
const _ETIMEDOUT_xnu = 60

// Apple libc pthread entry points, resolved by cosmoSemaInit at
// startup. Read by the cosmo_pthread_*_trampoline functions in
// sys_cosmo_arm64.s.
var (
	cosmoPthreadMutexInitFn     uintptr
	cosmoPthreadMutexLockFn     uintptr
	cosmoPthreadMutexUnlockFn   uintptr
	cosmoPthreadCondInitFn      uintptr
	cosmoPthreadCondWaitFn      uintptr
	cosmoPthreadCondTimedwaitFn uintptr
	cosmoPthreadCondSignalFn    uintptr

	// cosmoPthreadCondTimedwaitRelative records whether
	// cosmoPthreadCondTimedwaitFn is pthread_cond_timedwait_relative_np
	// (takes a timespec RELATIVE to now; the Apple-specific variant
	// upstream darwin uses) or fell back to plain pthread_cond_timedwait
	// (absolute CLOCK_REALTIME deadline). libSystem has exported the _np
	// symbol for as long as Go has existed - upstream binds it
	// unconditionally at load time - so the fallback exists only to
	// keep a hypothetical future libSystem from wedging timed sleeps.
	cosmoPthreadCondTimedwaitRelative bool
)

var (
	dlsymNamePthreadMutexInit          = []byte("pthread_mutex_init\x00")
	dlsymNamePthreadMutexLock          = []byte("pthread_mutex_lock\x00")
	dlsymNamePthreadMutexUnlock        = []byte("pthread_mutex_unlock\x00")
	dlsymNamePthreadCondInit           = []byte("pthread_cond_init\x00")
	dlsymNamePthreadCondWait           = []byte("pthread_cond_wait\x00")
	dlsymNamePthreadCondTimedwaitRelNp = []byte("pthread_cond_timedwait_relative_np\x00")
	dlsymNamePthreadCondTimedwait      = []byte("pthread_cond_timedwait\x00")
	dlsymNamePthreadCondSignal         = []byte("pthread_cond_signal\x00")
)

// cosmoSemaInit resolves the pthread entry points M parking needs on
// XNU. Called from osArchInit (XNU hosts only), which runs from osinit,
// before any M can contend a lock and park.
func cosmoSemaInit() {
	cosmoPthreadMutexInitFn = cosmoDlsym(&dlsymNamePthreadMutexInit[0])
	cosmoPthreadMutexLockFn = cosmoDlsym(&dlsymNamePthreadMutexLock[0])
	cosmoPthreadMutexUnlockFn = cosmoDlsym(&dlsymNamePthreadMutexUnlock[0])
	cosmoPthreadCondInitFn = cosmoDlsym(&dlsymNamePthreadCondInit[0])
	cosmoPthreadCondWaitFn = cosmoDlsym(&dlsymNamePthreadCondWait[0])
	cosmoPthreadCondSignalFn = cosmoDlsym(&dlsymNamePthreadCondSignal[0])
	cosmoPthreadCondTimedwaitFn = cosmoDlsym(&dlsymNamePthreadCondTimedwaitRelNp[0])
	cosmoPthreadCondTimedwaitRelative = cosmoPthreadCondTimedwaitFn != 0
	if cosmoPthreadCondTimedwaitFn == 0 {
		cosmoPthreadCondTimedwaitFn = cosmoDlsym(&dlsymNamePthreadCondTimedwait[0])
	}
}

// Wrappers around the dlsym'd pthread functions, following upstream
// sys_darwin.go's pattern: cgo_unsafe_args makes &m the address of a
// contiguous argument block, which the ABI0 trampoline unpacks into C
// argument registers on the g0 stack that asmcgocall switched to.

// cosmoPthreadLibcCall wraps asmcgocall for the pthread wrappers
// below, recording the caller's g/PC/SP in m.libcall* so the CPU
// profiler can traceback from a SIGPROF that lands inside the C call
// (upstream sys_libc.go's libcCall, ported verbatim; sigprof's
// libcall unwind branch is enabled by usesLibcall listing cosmo).
//
//go:nosplit
func cosmoPthreadLibcCall(fn, arg unsafe.Pointer) int32 {
	// Leave caller's PC/SP/G around for traceback.
	gp := getg()
	var mp *m
	if gp != nil {
		mp = gp.m
	}
	if mp != nil && mp.libcallsp == 0 {
		mp.libcallg.set(gp)
		mp.libcallpc = sys.GetCallerPC()
		// sp must be the last, because once async cpu profiler finds
		// all three values to be non-zero, it will use them
		mp.libcallsp = sys.GetCallerSP()
	} else {
		// Make sure we don't reset libcallsp. This makes
		// libcCall reentrant; We remember the g/pc/sp for the
		// first call on an M, until that libcCall instance
		// returns.  Reentrance only matters for signals, as
		// libc never calls back into Go.  The tricky case is
		// where we call libcX from an M and record g/pc/sp.
		// Before that call returns, a signal arrives on the
		// same M and the signal handling code calls another
		// libc function.  We don't want that second libcCall
		// from within the handler to be recorded, and we
		// don't want that call's completion to zero
		// libcallsp.
		// We don't need to set libcall* while we're in a sighandler
		// (even if we're not currently in libc) because we block all
		// signals while we're handling a signal. That includes the
		// profile signal, which is the one that uses the libcall* info.
		mp = nil
	}
	res := asmcgocall(fn, arg)
	if mp != nil {
		mp.libcallsp = 0
	}
	return res
}

//go:nosplit
//go:cgo_unsafe_args
func pthread_mutex_init(m *pthreadmutex, attr unsafe.Pointer) int32 {
	return cosmoPthreadLibcCall(unsafe.Pointer(abi.FuncPCABI0(cosmo_pthread_mutex_init_trampoline)), unsafe.Pointer(&m))
}
func cosmo_pthread_mutex_init_trampoline()

//go:nosplit
//go:cgo_unsafe_args
func pthread_mutex_lock(m *pthreadmutex) int32 {
	return cosmoPthreadLibcCall(unsafe.Pointer(abi.FuncPCABI0(cosmo_pthread_mutex_lock_trampoline)), unsafe.Pointer(&m))
}
func cosmo_pthread_mutex_lock_trampoline()

//go:nosplit
//go:cgo_unsafe_args
func pthread_mutex_unlock(m *pthreadmutex) int32 {
	return cosmoPthreadLibcCall(unsafe.Pointer(abi.FuncPCABI0(cosmo_pthread_mutex_unlock_trampoline)), unsafe.Pointer(&m))
}
func cosmo_pthread_mutex_unlock_trampoline()

//go:nosplit
//go:cgo_unsafe_args
func pthread_cond_init(c *pthreadcond, attr unsafe.Pointer) int32 {
	return cosmoPthreadLibcCall(unsafe.Pointer(abi.FuncPCABI0(cosmo_pthread_cond_init_trampoline)), unsafe.Pointer(&c))
}
func cosmo_pthread_cond_init_trampoline()

//go:nosplit
//go:cgo_unsafe_args
func pthread_cond_wait(c *pthreadcond, m *pthreadmutex) int32 {
	return cosmoPthreadLibcCall(unsafe.Pointer(abi.FuncPCABI0(cosmo_pthread_cond_wait_trampoline)), unsafe.Pointer(&c))
}
func cosmo_pthread_cond_wait_trampoline()

// pthread_cond_timedwait calls cosmoPthreadCondTimedwaitFn, i.e.
// pthread_cond_timedwait_relative_np with t relative to now, or - only
// if the _np symbol ever disappears from libSystem - the plain absolute
// variant. The caller builds t to match cosmoPthreadCondTimedwaitRelative.
//
//go:nosplit
//go:cgo_unsafe_args
func pthread_cond_timedwait(c *pthreadcond, m *pthreadmutex, t *timespec) int32 {
	return cosmoPthreadLibcCall(unsafe.Pointer(abi.FuncPCABI0(cosmo_pthread_cond_timedwait_trampoline)), unsafe.Pointer(&c))
}
func cosmo_pthread_cond_timedwait_trampoline()

//go:nosplit
//go:cgo_unsafe_args
func pthread_cond_signal(c *pthreadcond) int32 {
	return cosmoPthreadLibcCall(unsafe.Pointer(abi.FuncPCABI0(cosmo_pthread_cond_signal_trampoline)), unsafe.Pointer(&c))
}
func cosmo_pthread_cond_signal_trampoline()

// semacreate creates a semaphore for the M: on XNU hosts it lazily
// initializes the M's pthread mutex/cond pair (like upstream darwin);
// the Linux futex word needs no initialization.
//
//go:nosplit
func semacreate(mp *m) {
	if !isdarwin() {
		return
	}
	if mp.initialized {
		return
	}
	mp.initialized = true
	if cosmoPthreadMutexInitFn == 0 || cosmoPthreadMutexLockFn == 0 ||
		cosmoPthreadMutexUnlockFn == 0 || cosmoPthreadCondInitFn == 0 ||
		cosmoPthreadCondWaitFn == 0 || cosmoPthreadCondTimedwaitFn == 0 ||
		cosmoPthreadCondSignalFn == 0 {
		// cosmoSemaInit runs from osinit, before any M can park; a
		// miss here means libSystem stopped exporting a pthread
		// symbol. Dying loudly beats parking on garbage.
		throw("semacreate: pthread symbols unresolved")
	}
	if err := pthread_mutex_init(&mp.mutex, nil); err != 0 {
		throw("pthread_mutex_init")
	}
	if err := pthread_cond_init(&mp.cond, nil); err != 0 {
		throw("pthread_cond_init")
	}
}

// semasleep waits on the current M's semaphore.
// If ns < 0, wait forever. Otherwise wait for at most ns nanoseconds.
// Returns 0 if woken, -1 if timed out.
//
//go:nosplit
func semasleep(ns int64) int32 {
	gp := getg()
	mp := gp.m
	if !isdarwin() {
		return futexsemasleep(mp, ns)
	}
	// XNU host: upstream os_darwin.go's semasleep, with the timedwait
	// fallback branch as the one addition.
	var start int64
	if ns >= 0 {
		start = nanotime()
	}
	if gp == mp.gsignal {
		// sema sleep/wakeup are implemented with pthreads, which are not async-signal-safe on Darwin.
		throw("semasleep on Darwin signal stack")
	}
	pthread_mutex_lock(&mp.mutex)
	for {
		if mp.count > 0 {
			mp.count--
			xnuSemaAcquired.Add(1) // wedge forensics, netpoll_cosmo_xnu.go
			pthread_mutex_unlock(&mp.mutex)
			return 0
		}
		if ns >= 0 {
			spent := nanotime() - start
			if spent >= ns {
				pthread_mutex_unlock(&mp.mutex)
				return -1
			}
			var t timespec
			if cosmoPthreadCondTimedwaitRelative {
				t.setNsec(ns - spent)
			} else {
				// Absolute CLOCK_REALTIME deadline for plain
				// pthread_cond_timedwait. Wall time can jump with
				// clock adjustments; for semaphore timeouts an
				// early/late wake is acceptable (callers recompute
				// and retry), and this path is unreachable while
				// libSystem exports the _np symbol.
				sec, nsec := walltime()
				t.setNsec(sec*1e9 + int64(nsec) + (ns - spent))
			}
			err := pthread_cond_timedwait(&mp.cond, &mp.mutex, &t)
			if err == _ETIMEDOUT_xnu {
				pthread_mutex_unlock(&mp.mutex)
				return -1
			}
			// Anything else (0 = signaled, or a spurious/interrupted
			// wake) loops: the count check decides, and the remaining
			// time is recomputed from start.
		} else {
			pthread_cond_wait(&mp.cond, &mp.mutex)
		}
	}
}

// semawakeup wakes up the M's semaphore.
//
//go:nosplit
func semawakeup(mp *m) {
	if !isdarwin() {
		futexsemawakeup(mp)
		return
	}
	if g := getg(); g == g.m.gsignal {
		// Not async-signal-safe; sigqueue's sigsend uses the sigNote
		// pipe instead (sigqueue_note_cosmo_arm64.go).
		throw("semawakeup on Darwin signal stack")
	}
	xnuSemaWakeEnter.Add(1) // wedge forensics, netpoll_cosmo_xnu.go
	pthread_mutex_lock(&mp.mutex)
	mp.count++
	if mp.count > 0 {
		pthread_cond_signal(&mp.cond)
	}
	pthread_mutex_unlock(&mp.mutex)
	xnuSemaWakeDone.Add(1)
}

// futexsemasleep implements semasleep on Linux hosts: a counting semaphore
// over the futex syscall, with mOS.waitsemacount as the futex word.
//
//go:nosplit
func futexsemasleep(mp *m, ns int64) int32 {
	var deadline int64
	if ns >= 0 {
		deadline = nanotime() + ns
	}
	for {
		v := atomic.Load(&mp.waitsemacount)
		if v > 0 {
			if atomic.Cas(&mp.waitsemacount, v, v-1) {
				return 0
			}
			continue
		}
		wait := int64(-1)
		if ns >= 0 {
			wait = deadline - nanotime()
			if wait <= 0 {
				return -1
			}
		}
		futexsleep(&mp.waitsemacount, 0, wait)
	}
}

// futexsemawakeup implements semawakeup on Linux hosts.
//
//go:nosplit
func futexsemawakeup(mp *m) {
	atomic.Xadd(&mp.waitsemacount, 1)
	futexwakeup(&mp.waitsemacount, 1)
}
