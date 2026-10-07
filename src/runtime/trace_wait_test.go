// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// A traceWait sleeper blocks its thread until another thread releases it,
// so the protocol exists only where there are threads: everywhere but
// single-threaded wasm.

//go:build !wasm || wasm.threads

package runtime_test

import (
	"runtime"
	"runtime/debug"
	"testing"
)

// TestTraceWaitLosesNoRelease runs the protocol traceAdvance and StartTrace
// use to sleep until another M changes what they wait for. The releaser
// runs on a second P while the waiter's M is blocked, and releases at every
// point of the waiter's prepare, check and sleep, so a lost wakeup hangs
// the test. The waiter's P stays held while it sleeps, as traceAdvance's
// does; the releasers traceAdvance waits for cannot be stopped mid-release,
// but this goroutine can, so the collector is off for the test lest a
// stop-the-world wait on the sleeping waiter.
func TestTraceWaitLosesNoRelease(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(2))
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	runtime.TraceWaitRounds(20000, false)
	runtime.TraceWaitRounds(20000, true)
}
