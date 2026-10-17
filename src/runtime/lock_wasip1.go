// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasip1

package runtime

import "unsafe"

// wasm has no support for threads yet. There is no preemption.
// See proposal: https://github.com/WebAssembly/threads
// A mutex is never contended. A goroutine waiting on a note parks, and
// the goroutine that wakes the note, or the note's timer, readies it.

const (
	mutex_unlocked = 0
	mutex_locked   = 1

	mutexMLocksDelta = 16
)

type mWaitList struct{}

func lockVerifyMSize() {}

func mutexContended(l *mutex) bool {
	return false
}

func lock(l *mutex) {
	lockWithRank(l, getLockRank(l))
}

func lock2(l *mutex) {
	if l.key == mutex_locked {
		// wasm is single-threaded so we should never
		// observe this.
		throw("self deadlock")
	}
	gp := getg()
	if gp.m.locks < 0 {
		throw("lock count")
	}
	gp.m.locks += mutexMLocksDelta
	l.key = mutex_locked
}

func unlock(l *mutex) {
	unlockWithRank(l)
}

func unlock2(l *mutex) {
	if l.key == mutex_unlocked {
		throw("unlock of unlocked lock")
	}
	gp := getg()
	gp.m.locks -= mutexMLocksDelta
	if gp.m.locks < 0 {
		throw("lock count")
	}
	l.key = mutex_unlocked
}

// One-time notifications. Every goroutine runs on one thread, so a note
// needs no atomics. Its key is note_cleared, note_woken, note_timeout, or
// the g parked in notetsleepg until notewakeup readies it. note_timeout
// marks a timer that fired before its waiter parked.
const (
	note_cleared = 0
	note_woken   = 1
	note_timeout = 2
)

func noteclear(n *note) {
	n.key = note_cleared
}

func notewakeup(n *note) {
	old := n.key
	if old == note_woken {
		print("notewakeup - double wakeup (", old, ")\n")
		throw("notewakeup - double wakeup")
	}
	n.key = note_woken
	if old != note_cleared && old != note_timeout {
		goready((*g)(unsafe.Pointer(old)), 1)
	}
}

// noteParkCommit names gp as the waiter on the note at nptr, unless the
// note is no longer cleared. It runs on g0 after gp stops. A goroutine can
// be preempted in gopark's prologue, so the waiter must not be published
// before this point: a wakeup in that window would ready a runnable g.
func noteParkCommit(gp *g, nptr unsafe.Pointer) bool {
	n := (*note)(nptr)
	if n.key != note_cleared {
		return false
	}
	n.key = uintptr(unsafe.Pointer(gp))
	return true
}

func notesleep(n *note) {
	throw("notesleep not supported by wasi")
}

func notetsleep(n *note, ns int64) bool {
	throw("notetsleep not supported by wasi")
	return false
}

// same as runtime·notetsleep, but called on user g (not g0)
func notetsleepg(n *note, ns int64) bool {
	gp := getg()
	if gp == gp.m.g0 {
		throw("notetsleepg on g0")
	}
	if n.key == note_woken {
		return true
	}
	if n.key != note_cleared {
		throw("notetsleepg - note already has a waiting g")
	}

	if ns < 0 {
		for n.key != note_woken {
			gopark(noteParkCommit, unsafe.Pointer(n), waitReasonZero, traceBlockGeneric, 1)
		}
		return true
	}
	timeout := new(timer)
	timeout.init(noteTimedOut, unsafe.Pointer(n))
	timeout.reset(nanotime()+ns, 0)
	gopark(noteParkCommit, unsafe.Pointer(n), waitReasonSleep, traceBlockSleep, 1)
	timeout.stop()
	if n.key == note_timeout {
		n.key = note_cleared
	}
	return n.key == note_woken
}

// noteTimedOut is the timer function of a timed notetsleepg. If the
// waiting g is parked on the note, it clears the note and readies the g,
// which then reports the timeout. If the g has not parked yet, it marks
// the note so the g does not park.
func noteTimedOut(arg any, _ uintptr, _ int64) {
	n := (*note)(arg.(unsafe.Pointer))
	if n.key == note_cleared {
		n.key = note_timeout
		return
	}
	if n.key == note_woken || n.key == note_timeout {
		return
	}
	waiter := (*g)(unsafe.Pointer(n.key))
	n.key = note_cleared
	goready(waiter, 1)
}

func beforeIdle(int64, int64) (*g, bool) {
	return nil, false
}

func checkTimeouts() {}
