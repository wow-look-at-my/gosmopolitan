// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

// Semaphore implementation for cosmo arm64. The host OS is only known at
// run time: on macOS the M's semaphore is a dispatch_semaphore obtained via
// the APE loader's Syslib; on Linux it is a counting semaphore built on the
// futex syscall. This is used by lock_sema.go for synchronization
// primitives.

import "internal/runtime/atomic"

// DISPATCH_TIME_FOREVER for infinite wait
const _DISPATCH_TIME_FOREVER = ^uint64(0)

// Assembly trampolines for dispatch_semaphore functions
// These are defined in sys_cosmo_arm64.s

//go:noescape
func dispatch_semaphore_create_trampoline(value int64) uintptr

//go:noescape
func dispatch_semaphore_signal_trampoline(sema uintptr) int64

//go:noescape
func dispatch_semaphore_wait_trampoline(sema uintptr, timeout uint64) int64

//go:noescape
func dispatch_walltime_trampoline(delta int64) uint64

// semacreate creates a semaphore for the M.
// Called from lock_sema.go
//
//go:nosplit
func semacreate(mp *m) {
	if !isdarwin() {
		// Linux: the futex word in mOS needs no initialization.
		return
	}
	if mp.waitsema != 0 {
		return
	}
	// Create semaphore with initial count 0
	sema := dispatch_semaphore_create_trampoline(0)
	if sema == 0 {
		// Can't create semaphore - this will cause problems
		// but we can't throw here (called too early)
		return
	}
	mp.waitsema = sema
}

// semasleep waits on the current M's semaphore.
// If ns < 0, wait forever. Otherwise wait for at most ns nanoseconds.
// Returns 0 if woken, -1 if timed out.
//
//go:nosplit
func semasleep(ns int64) int32 {
	mp := getg().m
	if !isdarwin() {
		return futexsemasleep(mp, ns)
	}
	if mp.waitsema == 0 {
		// No semaphore - this shouldn't happen after semacreate
		// Fall back to spinning
		if ns >= 0 {
			start := nanotime()
			for nanotime()-start < ns {
				procyield(1)
			}
			return -1
		}
		// Can't spin forever
		throw("semasleep without semaphore")
	}

	var timeout uint64
	if ns < 0 {
		// Wait forever - but in bounded slices rather than one
		// DISPATCH_TIME_FOREVER wait. A timed-out wait returns to this
		// loop and issues a FRESH dispatch_semaphore_wait, which
		// re-examines the semaphore count atomically; a signal that
		// arrived meanwhile is consumed and we return 0 exactly as if
		// the single infinite wait had seen it. Semantics are
		// identical - we still return ONLY on a genuine wakeup, which
		// lock2 depends on (its M sits in the mutex wait list until a
		// wake pops it; a spurious return would let it re-queue while
		// still linked). But if a wakeup is ever lost INSIDE a parked
		// wait - the failure shape of the rare macOS wedge, where an M
		// slept through its semawakeup on a lock that was provably
		// released - the next slice picks the count up and the stall
		// is bounded at ~50ms instead of forever.
		for {
			deadline := dispatch_walltime_trampoline(50e6) // now + 50ms
			if deadline == 0 {
				// Syslib entry missing (cannot happen with v1+
				// loaders): fall back to the unbounded wait.
				break
			}
			if dispatch_semaphore_wait_trampoline(mp.waitsema, deadline) == 0 {
				return 0 // woken
			}
		}
		timeout = _DISPATCH_TIME_FOREVER
	} else {
		// dispatch_semaphore_wait takes an ABSOLUTE dispatch_time_t.
		// Passing the relative ns directly (as this used to) made
		// every timed wait expire immediately: positive
		// dispatch_time_t values are mach absolute ticks since boot,
		// so a small value is a deadline in the distant past, and
		// timed semasleeps degraded into busy spinning.
		//
		// The Syslib does not export dispatch_time, but
		// dispatch_walltime(NULL, delta) (v1+) yields an absolute
		// wall-clock deadline now+delta, which
		// dispatch_semaphore_wait accepts. Wall time can jump with
		// clock adjustments; for semaphore timeouts that is
		// acceptable (callers retry), unlike firing instantly.
		timeout = dispatch_walltime_trampoline(ns)
		if timeout == 0 {
			// Syslib entry missing (cannot happen with v1+
			// loaders): keep the old degraded behavior rather
			// than waiting forever.
			timeout = uint64(ns)
		}
	}

	ret := dispatch_semaphore_wait_trampoline(mp.waitsema, timeout)
	if ret != 0 {
		return -1 // timed out
	}
	return 0 // woken
}

// semawakeup wakes up the M's semaphore.
//
//go:nosplit
func semawakeup(mp *m) {
	if !isdarwin() {
		futexsemawakeup(mp)
		return
	}
	if mp.waitsema == 0 {
		return
	}
	dispatch_semaphore_signal_trampoline(mp.waitsema)
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
