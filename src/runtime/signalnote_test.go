// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// A signalNote sleeper blocks its thread until another thread wakes it, so
// the protocol exists only where there are threads: everywhere but
// single-threaded wasm.

//go:build !wasm || wasm.threads

package runtime_test

import (
	"runtime"
	"runtime/debug"
	"testing"
)

// TestSignalNoteLosesNoWakeup runs the protocol that traceAdvance,
// StartTrace and the SIGQUIT crash relay use to sleep until another M
// changes what they wait for. The waker runs on a second P while the
// waiter's M is blocked, and wakes at every point of the waiter's arm,
// check and sleep, so a lost wakeup hangs the test. The timed rounds time
// out often, so they also cover a wakeup that races with a timeout. The
// waiter's P stays held while it sleeps, as traceAdvance's does; the Ms
// traceAdvance waits for cannot be stopped mid-wake, but this goroutine
// can, so the collector is off for the test lest a stop-the-world wait on
// the sleeping waiter.
func TestSignalNoteLosesNoWakeup(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(2))
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	runtime.SignalNoteRounds(20000, 0)
	runtime.SignalNoteRounds(20000, 20000)
}
