// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime_test

import (
	"runtime"
	"sync"
	"testing"
)

// TestWasmThreadsRunOnNewM exercises the GOWASM=threads thread-creation
// path end to end under the test runner: newosproc hands an M to a pool
// worker (spawned by wasm_exec_node.js), real Go code runs on it, and
// heap, channels and mutexes are coherent across the Ms.
//
// The code run on the worker M must not call syscall/js (or t.Log etc.,
// which writes through it): host calls are main-thread-only in this
// phase.
func TestWasmThreadsRunOnNewM(t *testing.T) {
	mainM := runtime.WasmThreadsCurMID()

	var mu sync.Mutex
	counter := 0
	ch := make(chan int64, 4)
	var workerM, nestedM int64

	runtime.WasmThreadsRunOnNewM(func() {
		workerM = runtime.WasmThreadsCurMID()
		mu.Lock()
		counter++
		mu.Unlock()
		ch <- workerM
		runtime.WasmThreadsRunOnNewM(func() {
			nestedM = runtime.WasmThreadsCurMID()
			mu.Lock()
			counter++
			mu.Unlock()
			ch <- nestedM
		})
	})

	// The M-identity assertions are only deterministic at GOMAXPROCS=1
	// (the default, and the CI configuration): under a multi-P scheduler
	// this test goroutine itself can start on - and be preempted and
	// rescheduled across - any M, so "the M observed before the spawn"
	// pins nothing. The heap/channel/mutex coherence checks below hold at
	// any GOMAXPROCS.
	strict := runtime.GOMAXPROCS(0) == 1
	if got := runtime.WasmThreadsCurMID(); strict && got != mainM {
		t.Fatalf("main goroutine moved Ms: %d != %d", got, mainM)
	}
	if strict && workerM == mainM {
		t.Fatalf("fn ran on the main M (%d)", mainM)
	}
	if strict && (nestedM == mainM || nestedM == workerM) {
		t.Fatalf("nested fn did not run on a third M: main %d, worker %d, nested %d", mainM, workerM, nestedM)
	}
	if got := <-ch; got != workerM {
		t.Fatalf("channel from worker M: got %d, want %d", got, workerM)
	}
	if got := <-ch; got != nestedM {
		t.Fatalf("channel from nested M: got %d, want %d", got, nestedM)
	}
	mu.Lock()
	c := counter
	mu.Unlock()
	if c != 2 {
		t.Fatalf("mutex counter: got %d, want 2", c)
	}

	// Spawn again: the parked worker Ms must be reused (mget), not new
	// pool workers. Deterministic only at GOMAXPROCS=1: under a multi-P
	// scheduler other Ms park and unpark concurrently (GC workers, timer
	// self-serves), so the re-spawn may legitimately be handed some OTHER
	// parked M than the two this test tracked.
	var againM int64
	runtime.WasmThreadsRunOnNewM(func() {
		againM = runtime.WasmThreadsCurMID()
	})
	if strict && againM == mainM {
		t.Fatalf("re-spawn ran on the main M (%d)", mainM)
	}
	if strict && againM != workerM && againM != nestedM {
		t.Fatalf("re-spawn did not reuse a parked M: got %d, want %d or %d", againM, workerM, nestedM)
	}
}
