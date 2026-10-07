// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements runtime support for signal handling.
//
// Most synchronization primitives are not available from
// the signal handler (it cannot block, allocate memory, or use locks)
// so the handler communicates with a processing goroutine
// via struct sig, below.
//
// sigsend is called by the signal handler to queue a new signal.
// signal_recv is called by the Go program to receive a newly queued signal.
//
// Synchronization between sigsend and signal_recv is based on the sig.state
// variable. It can be in three states:
// * sigReceiving means that signal_recv is blocked on sig.Note and there are
//   no new pending signals.
// * sigSending means that sig.mask *may* contain new pending signals,
//   signal_recv can't be blocked in this state.
// * sigIdle means that there are no new pending signals and signal_recv is not
//   blocked.
//
// Transitions between states are done atomically with CAS.
//
// When signal_recv is unblocked, it resets sig.Note and rechecks sig.mask.
// If several sigsends and signal_recv execute concurrently, it can lead to
// unnecessary rechecks of sig.mask, but it cannot lead to missed signals
// nor deadlocks.

//go:build !plan9 && !wasip1

package runtime

import (
	"internal/goos"
	"internal/runtime/atomic"
	"unsafe"
)

// sig handles communication between the signal handler and os/signal.
// Other than the inuse and recv fields, the fields are accessed atomically.
//
// The wanted and ignored fields are only written by one goroutine at
// a time; access is controlled by the handlers Mutex in os/signal.
// The fields are only read by that one goroutine and by the signal handler.
// We access them atomically to minimize the race between setting them
// in the goroutine calling os/signal and the signal handler,
// which may be running in a different thread. That race is unavoidable,
// as there is no connection between handling a signal and receiving one,
// but atomic instructions should minimize it.
var sig struct {
	note       note
	mask       [(_NSIG + 31) / 32]uint32
	wanted     [(_NSIG + 31) / 32]uint32
	ignored    [(_NSIG + 31) / 32]uint32
	recv       [(_NSIG + 31) / 32]uint32
	state      atomic.Uint32
	delivering atomic.Uint32
	inuse      bool

	// idleWaiters holds the goroutines parked in signalWaitUntilIdle,
	// under idleLock. idleWaiting is 1 while the list is non-empty, so
	// that sigsend can read it without the lock.
	idleLock    mutex
	idleWaiters gList
	idleWaiting atomic.Uint32
}

const (
	sigIdle = iota
	sigReceiving
	sigSending
)

// sigNoteUsed is set by cosmo's osArchInit when the host OS is XNU. It
// is written once at startup, before initsig installs any signal
// handler, and only read afterwards (including from the signal
// handler). It stays false on every other GOOS.
var sigNoteUsed bool

// usesSigNote reports whether sigqueue must use the pipe-based,
// async-signal-safe sigNote implementation instead of regular notes.
// That is required wherever M parking is pthread-based - pthread mutex
// operations are not async-signal-safe, so sigsend, which runs in the
// signal handler, cannot use notewakeup there. Constant true on
// darwin/ios; on cosmo the same M-parking design is used exactly when
// the host is XNU, so osArchInit decides at startup; constant false
// everywhere else.
func usesSigNote() bool {
	return goos.IsDarwin == 1 || goos.IsIos == 1 || sigNoteUsed
}

// sigsend delivers a signal from sighandler to the internal signal delivery queue.
// It reports whether the signal was sent. If not, the caller typically crashes the program.
// It runs from the signal handler, so it's limited in what it can do.
func sigsend(s uint32) bool {
	bit := uint32(1) << uint(s&31)
	if s >= uint32(32*len(sig.wanted)) {
		return false
	}

	sig.delivering.Add(1)
	// We are running in the signal handler; defer is not available.

	if w := atomic.Load(&sig.wanted[s/32]); w&bit == 0 {
		sigDeliveryDone()
		return false
	}

	// Add signal to outgoing queue.
	for {
		mask := sig.mask[s/32]
		if mask&bit != 0 {
			sigDeliveryDone()
			return true // signal already in queue
		}
		if atomic.Cas(&sig.mask[s/32], mask, mask|bit) {
			break
		}
	}

	// Notify receiver that queue has new bit.
	sigNotifyReceiver()

	sigDeliveryDone()
	return true
}

// sigNotifyReceiver tells signal_recv that there is something to look
// at: it wakes the receiver if it is blocked, and otherwise leaves
// sigSending for it to find. It runs from the signal handler, and from
// signalWaitUntilIdle.
func sigNotifyReceiver() {
	for {
		switch sig.state.Load() {
		default:
			throw("sigsend: inconsistent state")
		case sigIdle:
			if sig.state.CompareAndSwap(sigIdle, sigSending) {
				return
			}
		case sigSending:
			// notification already pending
			return
		case sigReceiving:
			if sig.state.CompareAndSwap(sigReceiving, sigIdle) {
				if usesSigNote() {
					sigNoteWakeup(&sig.note)
					return
				}
				notewakeup(&sig.note)
				return
			}
		}
	}
}

// sigDeliveryDone ends one sigsend delivery. When the last delivery in
// flight ends while signalWaitUntilIdle callers wait, it sends signal_recv
// round its loop once more, so that the receiver sees no delivery in
// flight and readies them. It runs from the signal handler.
func sigDeliveryDone() {
	if sig.delivering.Add(-1) == 0 && sig.idleWaiting.Load() != 0 {
		sigNotifyReceiver()
	}
}

// sigReadyIdleWaiters readies the signalWaitUntilIdle callers if no
// sigsend is in flight. signal_recv calls it each time it has entered
// sigReceiving and is about to block, which is the moment every signal
// it returned before has been processed by os/signal.
func sigReadyIdleWaiters() {
	if sig.idleWaiting.Load() == 0 || sig.delivering.Load() != 0 {
		return
	}
	lock(&sig.idleLock)
	waiters := sig.idleWaiters
	sig.idleWaiters = gList{}
	sig.idleWaiting.Store(0)
	unlock(&sig.idleLock)
	for !waiters.empty() {
		goready(waiters.pop(), 1)
	}
}

// Called to receive the next queued signal.
// Must only be called from a single goroutine at a time.
//
//go:linkname signal_recv os/signal.signal_recv
func signal_recv() uint32 {
	for {
		// Serve any signals from local copy.
		for i := uint32(0); i < _NSIG; i++ {
			if sig.recv[i/32]&(1<<(i&31)) != 0 {
				sig.recv[i/32] &^= 1 << (i & 31)
				return i
			}
		}

		// Wait for updates to be available from signal sender.
	Receive:
		for {
			switch sig.state.Load() {
			default:
				throw("signal_recv: inconsistent state")
			case sigIdle:
				if sig.state.CompareAndSwap(sigIdle, sigReceiving) {
					sigReadyIdleWaiters()
					if usesSigNote() {
						sigNoteSleep(&sig.note)
						break Receive
					}
					notetsleepg(&sig.note, -1)
					noteclear(&sig.note)
					break Receive
				}
			case sigSending:
				if sig.state.CompareAndSwap(sigSending, sigIdle) {
					break Receive
				}
			}
		}

		// Incorporate updates from sender into local copy.
		for i := range sig.mask {
			sig.recv[i] = atomic.Xchg(&sig.mask[i], 0)
		}
	}
}

// signalWaitUntilIdle waits until the signal delivery mechanism is idle.
// This is used to ensure that we do not drop a signal notification due
// to a race between disabling a signal and receiving a signal.
// This assumes that signal delivery has already been disabled for
// the signal(s) in question, and here we are just waiting to make sure
// that all the signals have been delivered to the user channels
// by the os/signal package.
//
// Although the signals we care about have been removed from sig.wanted,
// another thread may have received a signal, read sig.wanted, and not yet
// finished updating sig.mask and waking the receiver. So idle means both
// that no sigsend is in flight and that signal_recv has gone back to
// sigReceiving (the sigIdle state is really more like sigProcessing). The
// caller parks until signal_recv sees both at once (sigReadyIdleWaiters).
// It first sends the receiver round its loop, so that a receiver already
// blocked looks again; a sigsend still in flight does the same when it
// finishes (sigDeliveryDone).
//
//go:linkname signalWaitUntilIdle os/signal.signalWaitUntilIdle
func signalWaitUntilIdle() {
	if !sig.inuse {
		// No signal was ever enabled, so none can be in flight.
		return
	}
	lock(&sig.idleLock)
	sig.idleWaiters.push(getg())
	sig.idleWaiting.Store(1)
	gopark(sigIdleWaitPark, nil, waitReasonSignalDeliveryIdle, traceBlockGeneric, 1)
}

// sigIdleWaitPark is signalWaitUntilIdle's gopark callback. The caller
// is already waiting when the receiver is sent round, so signal_recv may
// ready it at once.
func sigIdleWaitPark(gp *g, _ unsafe.Pointer) bool {
	unlock(&sig.idleLock)
	sigNotifyReceiver()
	return true
}

// Must only be called from a single goroutine at a time.
//
//go:linkname signal_enable os/signal.signal_enable
func signal_enable(s uint32) {
	if !sig.inuse {
		// This is the first call to signal_enable. Initialize.
		sig.inuse = true // enable reception of signals; cannot disable
		if usesSigNote() {
			sigNoteSetup(&sig.note)
		} else {
			noteclear(&sig.note)
		}
	}

	if s >= uint32(len(sig.wanted)*32) {
		return
	}

	w := sig.wanted[s/32]
	w |= 1 << (s & 31)
	atomic.Store(&sig.wanted[s/32], w)

	i := sig.ignored[s/32]
	i &^= 1 << (s & 31)
	atomic.Store(&sig.ignored[s/32], i)

	sigenable(s)
}

// Must only be called from a single goroutine at a time.
//
//go:linkname signal_disable os/signal.signal_disable
func signal_disable(s uint32) {
	if s >= uint32(len(sig.wanted)*32) {
		return
	}
	sigdisable(s)

	w := sig.wanted[s/32]
	w &^= 1 << (s & 31)
	atomic.Store(&sig.wanted[s/32], w)
}

// Must only be called from a single goroutine at a time.
//
//go:linkname signal_ignore os/signal.signal_ignore
func signal_ignore(s uint32) {
	if s >= uint32(len(sig.wanted)*32) {
		return
	}
	sigignore(s)

	w := sig.wanted[s/32]
	w &^= 1 << (s & 31)
	atomic.Store(&sig.wanted[s/32], w)

	i := sig.ignored[s/32]
	i |= 1 << (s & 31)
	atomic.Store(&sig.ignored[s/32], i)
}

// sigInitIgnored marks the signal as already ignored. This is called at
// program start by initsig. In a shared library initsig is called by
// libpreinit, so the runtime may not be initialized yet.
//
//go:nosplit
func sigInitIgnored(s uint32) {
	i := sig.ignored[s/32]
	i |= 1 << (s & 31)
	atomic.Store(&sig.ignored[s/32], i)
}

// Checked by signal handlers.
//
//go:linkname signal_ignored os/signal.signal_ignored
func signal_ignored(s uint32) bool {
	i := atomic.Load(&sig.ignored[s/32])
	return i&(1<<(s&31)) != 0
}
