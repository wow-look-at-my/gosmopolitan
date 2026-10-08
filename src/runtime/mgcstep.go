// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import (
	"internal/goos"
	"internal/runtime/atomic"
)

// Budgeted GC mark step for embedder-driven (e.g. frame-driven) applications.

// gcMarkStepChunk is the granularity of one gcDrainN slice inside the budgeted mark step.
const gcMarkStepChunk = 64 << 10

// gcMarkStep performs up to budgetMs milliseconds of GC mark work, in small
// increments, so the call overruns the budget by at most one increment
// (typically well under a millisecond). It returns true if mark work remains
// - calling again with more budget will make further progress - and false
// otherwise.
func gcMarkStep(budgetMs float64) bool {
	// Record that the embedder is frame-aware, whether a cycle is active.
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
// on the system stack. With the goroutine parked in _Gwaiting so its stack
// remains scannable, banks the completed work as background scan credit,
// and signals a background completion point. This holds if it finishes the
// last of the mark work. It reports whether mark work remains.
func gcMarkStepBudgeted(budgetNs int64) bool {
	if goos.IsJs != 0 {
		// The host is explicitly donating idle time.
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
			// Re-check on the system stack, like gcAssistAlloc1: the mark phase could have been about to end.
			return
		}

		gcBeginWork()

		// gcDrainN requires the caller to be preemptible so this goroutine's stack may be scanned while it drains.
		casGToWaitingForSuspendG(gp, _Grunning, waitReasonGCAssistMarking)

		gcw := &gp.m.p.ptr().gcw
		for nanotime() < deadline && !gp.preempt && !gcCPULimiter.limiting() {
			if gcw.empty() && !gcMarkWorkAvailable() {
				break
			}
			workDone := gcDrainN(gcw, gcMarkStepChunk)
			if workDone > 0 {
				// Bank the completed work as background scan credit so the embedder's subsequent allocations draw on it instead.
				gcFlushBgCredit(workDone)
			}
		}

		casgstatus(gp, _Gwaiting, _Grunning)

		if gcEndWork() {
			completed = true
		}
	})

	if completed {
		// This was the last worker and there is no more work: reach the background completion point here, inside the donated budget.
		gcMarkDone()
	}

	return gcphase == _GCmark && atomic.Load(&gcBlackenEnabled) != 0 && gcMarkWorkAvailable()
}
