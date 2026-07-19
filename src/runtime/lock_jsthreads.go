// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime

// GOWASM=threads runtime locks: futex-style mutexes and notes over the
// wasm threads proposal's memory.atomic.wait32/notify instructions
// (futexsleep/futexwakeup in sys_wasmthreads.s). This is what makes Ms on
// different worker threads able to block and wake each other.
//
// Main-thread caveat (phase B2): the main M may futex-wait too. Node.js
// permits Atomics-style waits on the main thread, so this is sound under
// node, but while the main M is blocked the host event loop is stalled:
// no JavaScript timers or callbacks run until it wakes (a worker M wakes
// it via futexwakeup). Browsers forbid main-thread waits entirely, so
// GOWASM=threads with worker Ms is node-only in this phase. A fully
// non-blocking main-thread park is a later phase (B3).
//
// The event-loop machinery itself (event_js.go) only ever runs on the
// main M: beforeIdle below routes worker Ms to futex parking instead.

import (
	"internal/runtime/atomic"
	"unsafe"
)

const (
	mutex_unlocked = 0
	mutex_locked   = 1
	mutex_sleeping = 2

	active_spin     = 4
	active_spin_cnt = 30
	passive_spin    = 1
)

// We use the uintptr mutex.key and note.key as a uint32.
//
//go:nosplit
func key32(p *uintptr) *uint32 {
	return (*uint32)(unsafe.Pointer(p))
}

type mWaitList struct{}

func lockVerifyMSize() {}

func mutexContended(l *mutex) bool {
	return atomic.Load(key32(&l.key)) > mutex_locked
}

func lock(l *mutex) {
	lockWithRank(l, getLockRank(l))
}

// This is the classic futex mutex (see lock_futex.go's ancestry):
// possible lock states are mutex_unlocked, mutex_locked and
// mutex_sleeping. mutex_sleeping means that there is presumably at
// least one sleeping thread.
func lock2(l *mutex) {
	gp := getg()

	if gp.m.locks < 0 {
		throw("runtime·lock: lock count")
	}
	gp.m.locks++

	// Speculative grab for lock.
	v := atomic.Xchg(key32(&l.key), mutex_locked)
	if v == mutex_unlocked {
		return
	}

	// wait is either MUTEX_LOCKED or MUTEX_SLEEPING
	// depending on whether there is a thread sleeping
	// on this mutex. If we ever change l->key from
	// MUTEX_SLEEPING to some other value, we must be
	// careful to change it back to MUTEX_SLEEPING before
	// returning, to ensure that the sleeping thread gets
	// its wakeup call.
	wait := v

	// On a uniprocessor there is no point spinning: the owner cannot
	// make progress while we spin. numCPUStartup is 1 on wasm today,
	// so go straight to sleeping.
	spin := 0
	if numCPUStartup > 1 {
		spin = active_spin
	}
	for {
		// Try for lock, spinning.
		for i := 0; i < spin; i++ {
			for l.key == mutex_unlocked {
				if atomic.Cas(key32(&l.key), mutex_unlocked, wait) {
					return
				}
			}
			procyield(active_spin_cnt)
		}

		// Try for lock, rescheduling.
		for i := 0; i < passive_spin; i++ {
			for l.key == mutex_unlocked {
				if atomic.Cas(key32(&l.key), mutex_unlocked, wait) {
					return
				}
			}
			osyield()
		}

		// Sleep.
		v = atomic.Xchg(key32(&l.key), mutex_sleeping)
		if v == mutex_unlocked {
			return
		}
		wait = mutex_sleeping
		gp.m.blocked = true
		futexsleep(key32(&l.key), mutex_sleeping, -1)
		gp.m.blocked = false
	}
}

func unlock(l *mutex) {
	unlockWithRank(l)
}

func unlock2(l *mutex) {
	v := atomic.Xchg(key32(&l.key), mutex_unlocked)
	if v == mutex_unlocked {
		throw("unlock of unlocked lock")
	}
	if v == mutex_sleeping {
		futexwakeup(key32(&l.key), 1)
	}

	gp := getg()
	gp.m.locks--
	if gp.m.locks < 0 {
		throw("runtime·unlock: lock count")
	}
	if gp.m.locks == 0 && gp.preempt { // restore the preemption request in case we've cleared it in newstack
		gp.stackguard0 = stackPreempt
	}
}

// One-time notifications.

// noteGLock protects the gp field of all notes (the goroutine parked by
// notetsleepg(n, -1)).
var noteGLock mutex

func noteclear(n *note) {
	n.key = 0
	n.gp = 0
}

func notewakeup(n *note) {
	old := atomic.Xchg(key32(&n.key), 1)
	if old != 0 {
		print("notewakeup - double wakeup (", old, ")\n")
		throw("notewakeup - double wakeup")
	}
	// Wake an M blocked in notesleep/notetsleep.
	futexwakeup(key32(&n.key), 1)
	// Wake a goroutine parked by notetsleepg(n, -1), if any.
	lock(&noteGLock)
	gp := n.gp.ptr()
	n.gp = 0
	unlock(&noteGLock)
	if gp != nil {
		goready(gp, 1)
	}
}

func notesleep(n *note) {
	gp := getg()
	if gp != gp.m.g0 {
		throw("notesleep not on g0")
	}
	for atomic.Load(key32(&n.key)) == 0 {
		gp.m.blocked = true
		futexsleep(key32(&n.key), 0, -1)
		gp.m.blocked = false
	}
}

// May run with m.p==nil if called from notetsleep, so write barriers
// are not allowed.
//
//go:nosplit
//go:nowritebarrier
func notetsleep_internal(n *note, ns int64) bool {
	gp := getg()

	if ns < 0 {
		for atomic.Load(key32(&n.key)) == 0 {
			gp.m.blocked = true
			futexsleep(key32(&n.key), 0, -1)
			gp.m.blocked = false
		}
		return true
	}

	if atomic.Load(key32(&n.key)) != 0 {
		return true
	}

	deadline := nanotime() + ns
	for {
		gp.m.blocked = true
		futexsleep(key32(&n.key), 0, ns)
		gp.m.blocked = false
		if atomic.Load(key32(&n.key)) != 0 {
			break
		}
		now := nanotime()
		if now >= deadline {
			break
		}
		ns = deadline - now
	}
	return atomic.Load(key32(&n.key)) != 0
}

func notetsleep(n *note, ns int64) bool {
	gp := getg()
	if gp != gp.m.g0 && gp.m.preemptoff != "" {
		throw("notetsleep not on g0")
	}

	return notetsleep_internal(n, ns)
}

// same as runtime·notetsleep, but called on user g (not g0)
func notetsleepg(n *note, ns int64) bool {
	gp := getg()
	if gp == gp.m.g0 {
		throw("notetsleepg on g0")
	}

	if ns < 0 {
		// Park the goroutine instead of blocking the M. An untimed
		// notetsleepg can pend for the program's whole lifetime (e.g.
		// os/signal's loop, profile readers); on js the M it happens
		// to run on may be the main M, whose thread drives the host
		// event loop and must not block indefinitely.
		for atomic.Load(key32(&n.key)) == 0 {
			gopark(notetsleepgPark, unsafe.Pointer(n), waitReasonZero, traceBlockGeneric, 1)
		}
		return true
	}

	// Timed waits keep the futex path (rare in the js port). This blocks
	// the M until the note is woken or the timeout elapses.
	entersyscallblock()
	ok := notetsleep_internal(n, ns)
	exitsyscall()
	return ok
}

// notetsleepgPark is the gopark callback of notetsleepg: it publishes the
// parked g on the note under noteGLock, unless the note was woken in the
// meantime (then the park is abandoned).
func notetsleepgPark(gp *g, np unsafe.Pointer) bool {
	n := (*note)(np)
	lock(&noteGLock)
	if atomic.Load(key32(&n.key)) != 0 {
		unlock(&noteGLock)
		return false // already woken, do not park
	}
	if n.gp != 0 {
		unlock(&noteGLock)
		throw("notetsleepg - note already has a waiting g")
	}
	n.gp.set(gp)
	unlock(&noteGLock)
	return true
}

// checkTimeouts is a no-op under GOWASM=threads: the deadline-note list it
// scans without GOWASM=threads (see lock_js.go) does not exist here, since
// notetsleepg uses futex timeouts instead of event-loop timeouts.
func checkTimeouts() {}

// wasmWorkerIdle is a futex word that is never woken; worker Ms use it for
// timed idle sleeps in beforeIdle while they hold the P and wait for the
// next timer to come due.
var wasmWorkerIdle uint32

// beforeIdle gets called by the scheduler if no goroutine is awake.
//
// On the main M it runs the shared event-loop integration (event_js.go):
// pause to the host, arm timeouts, resume returned event handlers.
//
// On a worker M there is no event loop. If timers are pending, sleep on a
// futex until (at most) the earliest deadline and report otherReady so
// findRunnable loops and fires them; the sleep is capped so the M
// re-examines the world regularly. (With GOMAXPROCS clamped to 1 the P
// this M holds cannot be wanted by anyone able to run while we sleep -
// only the P's owner can make work.) With no timers, fall through so the
// M parks in stopm on its futex note, to be woken by notewakeup from
// another thread.
//
//go:yeswritebarrierrec
func beforeIdle(now, pollUntil int64) (gp *g, otherReady bool) {
	if getg().m == &m0 {
		return eventBeforeIdle(now, pollUntil)
	}
	if pollUntil != 0 {
		if now == 0 {
			now = nanotime()
		}
		ns := pollUntil - now
		if ns > 10e6 {
			ns = 10e6 // cap: re-check the scheduler state regularly
		}
		if ns > 0 {
			futexsleep(&wasmWorkerIdle, 0, ns)
		}
		return nil, true
	}
	return nil, false
}
