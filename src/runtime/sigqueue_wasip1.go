// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements runtime support for signal handling on wasip1.
//
// WASI preview 1 has no mechanism for delivering signals to a running
// module: _NSIG is 0, initsig installs no handlers, and no host API
// exists that could raise a signal asynchronously. The generic sigqueue
// implementation would make the os/signal receiver goroutine block in
// notetsleepg on a note that can never be woken, and on wasip1 notes
// can only busy-wait (see notetsleepg in lock_wasip1.go): a single
// signal.Notify call would spin at 100% CPU for the life of the
// process and, by keeping a goroutine permanently runnable, would also
// disable deadlock detection for the whole program.
//
// Instead, signal_recv parks the receiver goroutine forever. The
// goroutine stays in _Gwaiting, so it costs nothing while the
// scheduler idles in poll_oneoff, and checkdead still reports
// "all goroutines are asleep - deadlock!" for genuinely deadlocked
// programs.

//go:build wasip1

package runtime

import _ "unsafe" // for go:linkname

// Called to receive the next queued signal.
// Must only be called from a single goroutine at a time.
//
//go:linkname signal_recv os/signal.signal_recv
func signal_recv() uint32 {
	// No signal can ever be delivered on wasip1: park forever.
	gopark(nil, nil, waitReasonZero, traceBlockGeneric, 1)
	throw("unreachable") // the goroutine above is never made runnable again
	return 0
}

// signalWaitUntilIdle waits until the signal delivery mechanism is idle.
// No signal is ever delivered on wasip1, so delivery is always idle.
//
//go:linkname signalWaitUntilIdle os/signal.signalWaitUntilIdle
func signalWaitUntilIdle() {
}

// Must only be called from a single goroutine at a time.
//
//go:linkname signal_enable os/signal.signal_enable
func signal_enable(s uint32) {
}

// Must only be called from a single goroutine at a time.
//
//go:linkname signal_disable os/signal.signal_disable
func signal_disable(s uint32) {
}

// Must only be called from a single goroutine at a time.
//
//go:linkname signal_ignore os/signal.signal_ignore
func signal_ignore(s uint32) {
}

//go:linkname signal_ignored os/signal.signal_ignored
func signal_ignored(s uint32) bool {
	return false
}
