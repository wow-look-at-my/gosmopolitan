// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package js

import (
	"sync"
	"sync/atomic"
)

// GOWASM=threads: JavaScript values live on the main thread. Worker-thread
// Ms (see the runtime's GOWASM=threads support) have every syscall/js host
// import stubbed out, so a call from a goroutine running on a worker M
// cannot work directly. mainThreadOp therefore MIGRATES the calling
// goroutine to the main thread (runtimeMigrateToMain: the runtime parks it
// and the main M's scheduler picks it up directly), after which the
// operation proceeds normally; the goroutine simply continues on the main
// thread. Only when migration cannot ever succeed - the main thread is
// exclusively bound to a different locked goroutine - does it panic, with
// a message naming the limitation, instead of dying in an opaque
// JavaScript exception on the worker.
//
// This is deliberately minimal main-thread routing: each blocked-then
// -rescheduled goroutine pays one migration per bounce back to a worker.
// Full host-call forwarding/affinity is a later phase.
//
// Without GOWASM=threads runtimeOnWorkerThread is always false and the
// guard is a cheap no-op.

// runtimeOnWorkerThread reports whether the calling goroutine currently
// runs on a worker-thread M under GOWASM=threads. Implemented in the
// runtime (event_js.go).
func runtimeOnWorkerThread() bool

// runtimeThreadsEnabled reports whether this program was built with
// GOWASM=threads at all (a build-time constant, unlike the per-call
// runtimeOnWorkerThread). Implemented in the runtime (event_js.go).
func runtimeThreadsEnabled() bool

// runtimeMigrateToMain moves the calling goroutine to the main thread,
// or reports that it cannot (main thread locked to another goroutine).
// Implemented in the runtime (event_js.go).
func runtimeMigrateToMain() bool

// runtimeBeginMainOp marks the calling goroutine main-thread-only (a
// nesting counter): while marked, the scheduler will only ever RESUME it
// on the main M (a worker M's schedule hands it to the migrate queue
// instead of running it). runtimeEndMainOp closes the region. Implemented
// in the runtime (event_js.go).
func runtimeBeginMainOp()

// runtimeEndMainOp closes a runtimeBeginMainOp region.
func runtimeEndMainOp()

// endMainOp is what mainThreadOp returns without GOWASM=threads: a no-op,
// so the defer costs nothing but the call.
func endMainOp() {}

// mainThreadOp begins a main-thread operation region and returns the
// function that ends it; every syscall/js entry point that touches host
// imports runs as
//
//	defer mainThreadOp("Value.Get")()
//
// Without GOWASM=threads this is a no-op. With it, the region guarantees
// every host import call inside executes on the main thread:
//
//  1. The goroutine is marked main-thread-only FIRST. The mark does not
//     teleport - it constrains where the goroutine resumes after any
//     preemption or park - so step 2 handles the current location.
//  2. If currently on a worker M, migrate (park onto the migrate queue;
//     only the main M pops it). After this check the goroutine is on the
//     main M, and because of the mark every later reschedule returns it
//     there: the "confirmed on main" fact can no longer be invalidated
//     by a loop-gate yield, stack-growth preempt, or GC-assist park
//     between the check and the host call (the TOCTOU that made worker
//     instances throw "finalizeRef/valueGet called on a worker
//     instance" under GC-heavy multi-P load).
//
// Only when migration cannot ever succeed - the main thread is
// exclusively bound to a different locked goroutine - does it panic,
// with a message naming the limitation, instead of dying in an opaque
// JavaScript exception on the worker.
func mainThreadOp(op string) func() {
	if !runtimeThreadsEnabled() {
		return endMainOp
	}
	runtimeBeginMainOp()
	if runtimeOnWorkerThread() {
		if !runtimeMigrateToMain() {
			runtimeEndMainOp()
			panic("syscall/js: " + op + " called from a goroutine on a worker thread while the " +
				"main thread is locked to another goroutine: GOWASM=threads keeps JavaScript " +
				"values and the event loop on the main thread, and this goroutine cannot be " +
				"moved there right now (worker-thread host-call forwarding is not implemented yet)")
		}
	}
	if pendingFinalizeCount.Load() != 0 {
		drainPendingFinalizers()
	}
	return runtimeEndMainOp
}

// Value finalizers (makeValue) release JavaScript refs via the
// finalizeRef host import. The GC runs finalizers on an arbitrary M, so
// under GOWASM=threads a finalizer can fire on a worker thread, where the
// import is unavailable. Such refs are queued here and released on the
// main thread by the next syscall/js operation.
var (
	pendingFinalizeMu    sync.Mutex
	pendingFinalizeRefs  []ref
	pendingFinalizeCount atomic.Int32
)

func queueFinalizeRef(r ref) {
	pendingFinalizeMu.Lock()
	pendingFinalizeRefs = append(pendingFinalizeRefs, r)
	pendingFinalizeMu.Unlock()
	pendingFinalizeCount.Add(1)
}

// drainPendingFinalizers releases the queued refs via the finalizeRef
// host import. Only called inside a mainThreadOp region, so every
// finalizeRef executes on the main thread even if the goroutine is
// preempted mid-loop (with the queue hot after GC-heavy tests this loop
// is by far the longest run of host calls in the package - it is where
// the pre-mark TOCTOU actually fired).
func drainPendingFinalizers() {
	pendingFinalizeMu.Lock()
	refs := pendingFinalizeRefs
	pendingFinalizeRefs = nil
	pendingFinalizeCount.Store(0)
	pendingFinalizeMu.Unlock()
	for _, r := range refs {
		finalizeRef(r)
	}
}
