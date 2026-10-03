// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goos"
	"internal/runtime/atomic"
)

// Budgeted GC mark step for embedder-driven (e.g. frame-driven)
// applications.
//
// An embedder that drives the program from an external loop (the
// canonical case is a js/wasm animation-frame application, where the
// runtime cannot run concurrently with the host at all) can donate its
// leftover per-iteration time budget to the garbage collector: the mark
// step performs up to that much GC mark work, off the application's
// critical path. The logic is platform-independent; js/wasm exposes it to
// the host as the go_gc_mark_step wasm export (mgcstep_js.go), and the
// portable entry points are exercised directly by runtime tests on every
// platform.

// gcMarkStepChunk is the granularity of one gcDrainN slice inside the
// budgeted mark step: small enough that the deadline is checked every few
// hundred microseconds of scanning even at wasm speeds, large enough that
// the loop overhead is negligible.
const gcMarkStepChunk = 64 << 10

// gcMarkStep performs up to budgetMs milliseconds of GC mark work, in
// small increments, so the call overruns the budget by at most one
// increment (typically well under a millisecond). It returns true if mark
// work remains - calling again with more budget will make further
// progress - and false otherwise. When no GC cycle is active it is a
// cheap no-op that returns false. If the budget suffices to finish the
// remaining mark work, the cycle's mark termination runs inside this
// call.
//
// Calling gcMarkStep also records the embedder as "frame-aware" for a
// while (gcControllerState.lastMarkStepTime): the pacer then caps the
// fractional mark worker's in-frame quota and relies on these donations
// instead (see startCycle).
func gcMarkStep(budgetMs float64) bool {
	// Record that the embedder is frame-aware, whether or not a cycle is
	// active.
	gcController.lastMarkStepTime.Store(nanotime())

	const maxBudgetMs = 1000
	if budgetMs > maxBudgetMs {
		budgetMs = maxBudgetMs
	}
	if budgetMs <= 0 || gcphase != _GCmark {
		return gcphase == _GCmark && gcMarkWorkAvailable()
	}
	return gcMarkStepBudgeted(int64(budgetMs * 1e6))
}

// gcMarkStepBudgeted performs up to budgetNs nanoseconds of GC mark work,
// following the same discipline as gcAssistAlloc1: it drains via gcDrainN
// on the system stack with the goroutine parked in _Gwaiting so its stack
// remains scannable, banks the completed work as background scan credit,
// and signals a background completion point if it finishes the last of the
// mark work. It reports whether mark work remains.
func gcMarkStepBudgeted(budgetNs int64) bool {
	if goos.IsJs != 0 {
		// The host is explicitly donating idle time, so idle marking need
		// not stay throttled (see wasmIdleMarkYield in mgcmark.go).
		wasmIdleMarkYield = false
	}

	if atomic.Load(&gcBlackenEnabled) == 0 {
		return false
	}

	gp := getg()
	deadline := nanotime() + budgetNs
	completed := false

	systemstack(func() {
		if atomic.Load(&gcBlackenEnabled) == 0 {
			// Re-check on the system stack, like gcAssistAlloc1: the
			// mark phase could have been about to end.
			return
		}

		gcBeginWork()

		// gcDrainN requires the caller to be preemptible so this
		// goroutine's stack may be scanned while it drains.
		casGToWaitingForSuspendG(gp, _Grunning, waitReasonGCAssistMarking)

		gcw := &gp.m.p.ptr().gcw
		for nanotime() < deadline && !gp.preempt && !gcCPULimiter.limiting() {
			if gcw.empty() && !gcMarkWorkAvailable() {
				break
			}
			workDone := gcDrainN(gcw, gcMarkStepChunk)
			if workDone > 0 {
				// Bank the completed work as background scan credit so
				// the embedder's subsequent allocations draw on it
				// instead of assisting synchronously.
				gcFlushBgCredit(workDone)
			}
		}

		casgstatus(gp, _Gwaiting, _Grunning)

		if gcEndWork() {
			completed = true
		}
	})

	if completed {
		// This was the last worker and there is no more work: reach the
		// background completion point here, inside the donated budget, so
		// the mark termination stop-the-world does not land inside the
		// embedder's critical path (e.g. inside a frame).
		gcMarkDone()
	}

	return gcphase == _GCmark && atomic.Load(&gcBlackenEnabled) != 0 && gcMarkWorkAvailable()
}
