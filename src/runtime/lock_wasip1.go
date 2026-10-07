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
// needs no atomics. Its key is note_cleared, note_woken, or the g parked
// in notetsleepg until notewakeup readies it.
const (
	note_cleared = 0
	note_woken   = 1
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
	if old != note_cleared {
		goready((*g)(unsafe.Pointer(old)), 1)
	}
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

	n.key = uintptr(unsafe.Pointer(gp))
	if ns < 0 {
		gopark(nil, nil, waitReasonZero, traceBlockGeneric, 1)
		return true
	}
	timeout := new(timer)
	timeout.init(noteTimedOut, unsafe.Pointer(n))
	timeout.reset(nanotime()+ns, 0)
	gopark(nil, nil, waitReasonSleep, traceBlockSleep, 1)
	timeout.stop()
	return n.key == note_woken
}

// noteTimedOut is the timer function of a timed notetsleepg. If the
// waiting g is still parked on the note, it clears the note and readies
// the g, which then reports the timeout.
func noteTimedOut(arg any, _ uintptr, _ int64) {
	n := (*note)(arg.(unsafe.Pointer))
	if n.key == note_cleared || n.key == note_woken {
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
