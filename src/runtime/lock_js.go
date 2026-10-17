// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && !wasm.threads

package runtime

import "unsafe"

// js/wasm without GOWASM=threads has no support for threads. There is no
// preemption.
//
// The JavaScript event-loop integration (pause/resume, the exit-time
// deadlock probe) lives in event_js.go, shared with the GOWASM=threads
// runtime.

const (
	mutex_unlocked = 0
	mutex_locked   = 1

	note_cleared = 0
	note_woken   = 1
	note_timeout = 2

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
		// js/wasm is single-threaded so we should never
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

// One-time notifications.

// Linked list of notes with a deadline.
var allDeadlineNotes *note

func noteclear(n *note) {
	n.status = note_cleared
}

func notewakeup(n *note) {
	if n.status == note_woken {
		throw("notewakeup - double wakeup")
	}
	cleared := n.status == note_cleared
	n.status = note_woken
	if cleared && n.gp != nil {
		goready(n.gp, 1)
	}
}

// noteParkCommit names gp as the waiter on the note at nptr, unless the
// note is no longer cleared. It runs on g0 after gp stops. A goroutine can
// be preempted in gopark's prologue, so the waiter must not be published
// before this point: a wakeup in that window would ready a runnable g.
func noteParkCommit(gp *g, nptr unsafe.Pointer) bool {
	n := (*note)(nptr)
	if n.status != note_cleared {
		return false
	}
	n.gp = gp
	return true
}

func notesleep(n *note) {
	throw("notesleep not supported by js")
}

func notetsleep(n *note, ns int64) bool {
	throw("notetsleep not supported by js")
	return false
}

// same as runtime·notetsleep, but called on user g (not g0)
func notetsleepg(n *note, ns int64) bool {
	gp := getg()
	if gp == gp.m.g0 {
		throw("notetsleepg on g0")
	}

	if ns >= 0 {
		deadline := nanotime() + ns
		delay := ns/1000000 + 1 // round up
		if delay > 1<<31-1 {
			delay = 1<<31 - 1 // cap to max int32
		}

		id := scheduleTimeoutEvent(delay)

		n.deadline = deadline
		if allDeadlineNotes != nil {
			allDeadlineNotes.allprev = n
		}
		n.allnext = allDeadlineNotes
		allDeadlineNotes = n

		gopark(noteParkCommit, unsafe.Pointer(n), waitReasonSleep, traceBlockSleep, 1)

		clearTimeoutEvent(id) // note might have woken early, clear timeout

		n.gp = nil
		n.deadline = 0
		if n.allprev != nil {
			n.allprev.allnext = n.allnext
		}
		if allDeadlineNotes == n {
			allDeadlineNotes = n.allnext
		}
		n.allprev = nil
		n.allnext = nil

		return n.status == note_woken
	}

	for n.status != note_woken {
		gopark(noteParkCommit, unsafe.Pointer(n), waitReasonZero, traceBlockGeneric, 1)

		n.gp = nil
	}
	return true
}

// checkTimeouts resumes goroutines that are waiting on a note which has reached its deadline.
// A note whose waiter has not parked yet only gets the timeout status.
func checkTimeouts() {
	now := nanotime()
	for n := allDeadlineNotes; n != nil; n = n.allnext {
		if n.status == note_cleared && n.deadline != 0 && now >= n.deadline {
			n.status = note_timeout
			if n.gp != nil {
				goready(n.gp, 1)
			}
		}
	}
}

// beforeIdle gets called by the scheduler if no goroutine is awake.
// It delegates to the shared event-loop integration in event_js.go.
//
//go:yeswritebarrierrec
func beforeIdle(now, pollUntil int64) (gp *g, otherReady bool) {
	return eventBeforeIdle(now, pollUntil)
}

// wasmThreadsHandleEventEntry is the GOWASM=threads hook at the top of
// handleEvent (see lock_jsthreads.go). Without threads the main M never
// parks in the event loop from g0, so there is nothing to intercept.
func wasmThreadsHandleEventEntry() bool {
	return false
}
