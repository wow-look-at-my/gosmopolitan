// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime_test

import (
	"runtime"
	"sync"
	"testing"
	"time"
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

	// Spawn again: with parked worker Ms available, the scheduler must
	// reuse one (mget) instead of growing the pool with a fresh M.
	//
	// Two things make the obvious assertions racy, so neither is used:
	//
	//   - WasmThreadsRunOnNewM returning does not mean the M that ran fn
	//     has parked - the M reaches sched.midle through its scheduler
	//     tail (startlockedm -> stopm -> mput) on its own host thread,
	//     concurrently with this resumed goroutine. So the re-spawn must
	//     first WAIT until worker Ms are actually reusable.
	//
	//   - WHICH parked M mget returns is not deterministic even at
	//     GOMAXPROCS=1: a parked M's watchdog tick re-mputs it to the
	//     head of the idle-M LIFO, and earlier tests (or earlier -count
	//     iterations) may have left additional pool Ms parked, any of
	//     which is a legitimate pick. So reuse is asserted as "the M
	//     count did not grow", not as id-membership.
	//
	// The wait uses Gosched, not Sleep: a sleep timer gets self-served
	// by a parked worker's watchdog, which would migrate this goroutine
	// onto a worker M; Gosched keeps the P held here. A runtime that
	// never parks worker Ms fails loudly and deterministically below.
	if strict {
		waitStart := time.Now()
		for runtime.WasmThreadsIdleWorkerMs() < 2 {
			if time.Since(waitStart) > 10*time.Second {
				t.Fatalf("worker Ms never parked: %d parked after 10s", runtime.WasmThreadsIdleWorkerMs())
			}
			runtime.Gosched()
		}
	}
	mBefore := runtime.WasmThreadsMCount()
	var againM int64
	runtime.WasmThreadsRunOnNewM(func() {
		againM = runtime.WasmThreadsCurMID()
	})
	if strict && againM == mainM {
		t.Fatalf("re-spawn ran on the main M (%d)", mainM)
	}
	if mAfter := runtime.WasmThreadsMCount(); strict && mAfter != mBefore {
		t.Fatalf("re-spawn did not reuse a parked M: M count grew %d -> %d (fn ran on M %d)", mBefore, mAfter, againM)
	}
}
