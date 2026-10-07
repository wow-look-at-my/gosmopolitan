// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build darwin

package runtime

// sigSafeEvent is a wakeup that a signal handler can send: an EVFILT_USER
// event on a kqueue of its own. kevent takes no lock in user space, so it
// is async-signal-safe, unlike the pthread mutex and condition variable a
// note sleeps on here. Wakes sent before a sleep collapse into one
// (EV_CLEAR), so sleep can return for a wake an earlier waker sent, and
// callers check their condition again after it returns. A kqueue is not
// inherited by fork, so a child never holds the descriptor.
type sigSafeEvent struct {
	kq int32
}

// sigSafeEventNeeded reports whether a note cannot be woken from a
// signal handler on this host, so that a wait a handler ends must use a
// sigSafeEvent.
func sigSafeEventNeeded() bool {
	return true
}

// init creates the kqueue and reports whether it succeeded. It is
// called once, before any wake or sleep.
func (e *sigSafeEvent) init() bool {
	kq := kqueue()
	if kq < 0 {
		return false
	}
	closeonexec(kq)
	addWakeupEvent(kq)
	e.kq = kq
	return true
}

// wake triggers the event. It is async-signal-safe.
func (e *sigSafeEvent) wake() {
	wakeNetpoll(e.kq)
}

// sleep blocks the calling thread until the event is triggered, or until
// ns nanoseconds pass if ns >= 0. It reports whether the event was
// triggered.
func (e *sigSafeEvent) sleep(ns int64) bool {
	var ts timespec
	var timeout *timespec
	if ns >= 0 {
		ts.setNsec(ns)
		timeout = &ts
	}
	var ev keventt
	for {
		num := kevent(e.kq, nil, 0, &ev, 1, timeout)
		if num > 0 {
			return true
		}
		if num == 0 {
			return false
		}
		if num != -_EINTR {
			println("runtime: sigSafeEvent kevent failed with", -num)
			throw("runtime: sigSafeEvent kevent failed")
		}
		if timeout != nil {
			// The remaining time is not known; the caller checks its
			// condition and its own deadline again.
			return false
		}
	}
}
