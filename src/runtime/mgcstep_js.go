// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package runtime

import "internal/runtime/atomic"

// Budgeted GC mark step for frame-driven js/wasm applications.
//
// On js/wasm the runtime cannot run concurrently with the host: all GC mark
// work happens either synchronously inside allocation (assists), during
// scheduler passes while Go code runs, or in idle drains that block the
// event loop. For an animation-frame application that means mark work lands
// inside frames. The go_gc_mark_step export lets the host donate its idle
// time between frames instead: JavaScript calls it with the frame budget it
// has left over, and the runtime performs up to that much mark work, off
// the application's critical path.

// gcMarkStepChunk is the granularity of one gcDrainN slice inside the
// budgeted mark step: small enough that the deadline is checked every few
// hundred microseconds of wasm-speed scanning, large enough that the loop
// overhead is negligible.
const gcMarkStepChunk = 64 << 10

// wasm_gc_mark_step implements the "go_gc_mark_step" wasm export, the
// JS-visible contract being:
//
//	go_gc_mark_step(budgetMs: number) -> boolean
//
// If a GC mark phase is in progress, it performs up to budgetMs
// milliseconds of GC mark work, in small increments, so the call overruns
// the budget by at most one increment (typically well under a
// millisecond). It returns true if mark work remains - calling again
// with more budget will make further progress - and false otherwise.
// When no GC cycle is active it is a cheap no-op that returns false.
// If the budget suffices to finish the remaining mark work, the cycle's
// mark termination runs inside this call, so the stop-the-world pause
// lands between frames rather than inside one. Like any other export, it
// must only be called while the Go program is paused (from the event
// loop between frames), after go.run(...) has started the program.
//
//go:wasmexport go_gc_mark_step
func wasm_gc_mark_step(budgetMs float64) bool {
	// Record that the host is frame-aware, whether or not a cycle is
	// active: the pacer then keeps background marking out of the host's
	// frames and relies on these donations instead (see startCycle).
	wasmLastMarkStepTime = nanotime()

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
	// The host is explicitly donating idle time, so idle marking need not
	// stay throttled (see wasmIdleMarkYield in mgcmark.go).
	wasmIdleMarkYield = false

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
				// allocations in the next frame draw on it instead of
				// assisting synchronously.
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
		// background completion point here, between frames, so the mark
		// termination stop-the-world does not land inside a frame.
		gcMarkDone()
	}

	return gcphase == _GCmark && atomic.Load(&gcBlackenEnabled) != 0 && gcMarkWorkAvailable()
}
