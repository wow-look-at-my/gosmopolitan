// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

// Semaphore implementation for cosmo arm64, used by lock_spinbit.go and
// lock_sema.go for M parking. The host OS is only known at run time. On
// Linux the M's semaphore is a counting semaphore over futex. On XNU it
// is upstream os_darwin.go's design ported verbatim: a per-M
// pthread_mutex and pthread_cond pair, dlsym'd from Apple libc.
//
// Never park an M on a Syslib dispatch semaphore: that loses wakeups
// under load, and it is why upstream darwin uses pthread primitives.
//
// A forked child's pthread state is undefined, but the child path
// between fork and execve is nosplit, lock-free and calls only
// pre-resolved async-signal-safe functions, so it never reaches these.

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

// cosmoPthreadLibcCall wraps asmcgocall for the pthread wrappers below,
// recording the caller's g/PC/SP in m.libcall* so the CPU profiler can
// traceback from a SIGPROF that lands inside the C call. This is
// upstream sys_libc.go's libcCall, and usesLibcall listing cosmo
// enables sigprof's libcall unwind branch.
//
// asmcgocall is required: pthread_mutex_lock and pthread_cond_wait are
// real C functions with real frames, unlike the shallow sysret wrappers
// elsewhere here, and semasleep runs on arbitrary stacks - a contended
// lock2 parks from a user g stack. asmcgocall switches to g0 first.
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
		// Do NOT reset libcallsp. Remember the g/pc/sp of the FIRST
		// call on an M until that instance returns, which makes this
		// reentrant. Reentrance only matters for signals, since libc
		// never calls back into Go: a signal arriving mid-call on the
		// same M whose handler calls libc must neither record itself
		// nor zero libcallsp on its way out. A sighandler needs no
		// libcall* of its own, because every signal is blocked while
		// one runs - including the profile signal, the one consumer.
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
