// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// This file implements runtime support for signal handling on wasip1.

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
