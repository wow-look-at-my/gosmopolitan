// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package runtime

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
// lands between frames rather than inside one. Budgets are clamped to
// 1000ms; budgetMs <= 0 performs no work and just reports whether work
// remains. Like any other export, it must only be called while the Go
// program is paused (from the event loop between frames), after
// go.run(...) has started the program.
//
// This is a thin shim over the portable budgeted mark step; see
// gcMarkStep in mgcstep.go.
//
//go:wasmexport go_gc_mark_step
func wasm_gc_mark_step(budgetMs float64) bool {
	return gcMarkStep(budgetMs)
}
