// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build js && wasm

package js

import (
	"sync"
	"sync/atomic"
)

// GOWASM=threads: JavaScript values live on the main thread.

// runtimeOnWorkerThread reports whether the calling goroutine runs on a worker-thread M under GOWASM=threads.
func runtimeOnWorkerThread() bool

// runtimeThreadsEnabled reports whether this program was built with GOWASM=threads at all.
func runtimeThreadsEnabled() bool

// runtimeMigrateToMain moves the calling goroutine to the main thread.
func runtimeMigrateToMain() bool

// runtimeBeginMainOp marks the calling goroutine main-thread-only (a nesting counter): while marked.
func runtimeBeginMainOp()

// runtimeEndMainOp closes a runtimeBeginMainOp region.
func runtimeEndMainOp()

// endMainOp is what mainThreadOp returns without GOWASM=threads: a no-op, so the defer costs nothing but the call.
func endMainOp() {}

// mainThreadOp begins a main-thread operation region and returns the
// function that ends it.
//
//	defer mainThreadOp("Value.Get")()
//
// Without GOWASM=threads this is a no-op.
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

// Value finalizers (makeValue) release JavaScript refs via the finalizeRef
// host import.
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

// drainPendingFinalizers releases the queued refs via the finalizeRef host
// import.
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
