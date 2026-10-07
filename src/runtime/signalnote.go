// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

// A signalNote is a one-waiter wakeup that a signal handler may post. The
// waiter arms it, re-checks its condition, and then sleeps on it or
// disarms it. A wake that finds the note armed disarms it and wakes the
// waiter. Each arming produces at most one wakeup, so the waiter never
// finds a stale wakeup left over from an earlier arming. The waiter calls
// arm, sleep and disarm on the system stack of one M.
//
// The sleeping half differs per OS, because it must be woken by a call
// that is async-signal-safe and needs no g: a handler for a signal that
// lands on a thread Go did not create has none. The signalNote type and its
// reset, post and await methods are declared per OS. Most systems sleep on
// the waiting M's semaphore. Darwin's semaphore takes a pthread mutex, so
// Darwin sleeps on a ulock, and cosmo, whose arm64 semaphore does the same
// on XNU, on a futex word.

// arm prepares n for one wakeup.
//
//go:systemstack
func (n *signalNote) arm() {
	n.reset()
	n.waiting.Store(1)
}

// sleep sleeps until a wake posts the armed n.
//
//go:systemstack
func (n *signalNote) sleep() {
	n.await()
}

// disarm withdraws an armed n whose waiter no longer needs to sleep. If a
// wake has already claimed n, disarm takes that wakeup so the next arming
// starts clean.
//
//go:systemstack
func (n *signalNote) disarm() {
	if !n.waiting.CompareAndSwap(1, 0) {
		n.await()
	}
}

// wake wakes the waiter if n is armed. It is async-signal-safe and needs no
// g or m.
//
//go:nosplit
//go:nowritebarrierrec
func (n *signalNote) wake() {
	if n.waiting.Load() == 0 || !n.waiting.CompareAndSwap(1, 0) {
		return
	}
	n.post()
}
