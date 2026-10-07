// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// sigSafeEvent is a wakeup that a signal handler can send on an XNU host,
// where M parking is pthread-based.
type sigSafeEvent struct {
	kq int32
}

// sigSafeEventNeeded reports whether a note cannot be woken from a signal
// handler on this host, so that a wait.
func sigSafeEventNeeded() bool {
	return sigNoteUsed
}

// init creates the kqueue and reports whether it succeeded. It is
// called once, before any wake or sleep.
func (e *sigSafeEvent) init() bool {
	if !isdarwin() || !cosmoDarwinKqueueSupported() {
		return false
	}
	kq, _ := cosmoDarwinKqueue()
	if kq < 0 {
		return false
	}
	closeonexec(kq)
	ev := keventt{
		ident:  xnuKqIdent,
		filter: _EVFILT_USER_xnu,
		flags:  _EV_ADD_xnu | _EV_CLEAR_xnu,
	}
	for {
		num, errno := cosmoDarwinKevent(kq, &ev, 1, nil, 0, nil)
		if num >= 0 {
			break
		}
		if errno != _EINTR {
			closefd(kq)
			return false
		}
	}
	e.kq = kq
	return true
}

// wake triggers the event. It is async-signal-safe.
func (e *sigSafeEvent) wake() {
	ev := keventt{
		ident:  xnuKqIdent,
		filter: _EVFILT_USER_xnu,
		fflags: _NOTE_TRIGGER_xnu,
	}
	for {
		num, errno := cosmoDarwinKevent(e.kq, &ev, 1, nil, 0, nil)
		if num >= 0 {
			return
		}
		if errno != _EINTR {
			println("runtime: sigSafeEvent kevent trigger failed with", errno)
			throw("runtime: sigSafeEvent kevent trigger failed")
		}
	}
}

// It reports whether the event was triggered.
func (e *sigSafeEvent) sleep(ns int64) bool {
	var ts timespec
	var timeout *timespec
	if ns >= 0 {
		ts.setNsec(ns)
		timeout = &ts
	}
	var ev keventt
	for {
		num, errno := cosmoDarwinKevent(e.kq, nil, 0, &ev, 1, timeout)
		if num > 0 {
			return true
		}
		if num == 0 {
			return false
		}
		if errno != _EINTR {
			println("runtime: sigSafeEvent kevent failed with", errno)
			throw("runtime: sigSafeEvent kevent failed")
		}
		if timeout != nil {
			// The remaining time is not known; the caller checks its condition and its own deadline again.
			return false
		}
	}
}
