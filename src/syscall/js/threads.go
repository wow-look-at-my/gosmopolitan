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
// cannot work directly. mustBeMainThread therefore MIGRATES the calling
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

// runtimeMigrateToMain moves the calling goroutine to the main thread,
// or reports that it cannot (main thread locked to another goroutine).
// Implemented in the runtime (event_js.go).
func runtimeMigrateToMain() bool

func mustBeMainThread(op string) {
	if runtimeOnWorkerThread() {
		if !runtimeMigrateToMain() {
			panic("syscall/js: " + op + " called from a goroutine on a worker thread while the " +
				"main thread is locked to another goroutine: GOWASM=threads keeps JavaScript " +
				"values and the event loop on the main thread, and this goroutine cannot be " +
				"moved there right now (worker-thread host-call forwarding is not implemented yet)")
		}
	}
	if pendingFinalizeCount.Load() != 0 {
		drainPendingFinalizers()
	}
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
