// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	. "runtime"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// The budgeted mark step (gcMarkStep, exported to js/wasm hosts as the
// go_gc_mark_step wasm export) is platform-independent. These tests
// exercise it on every platform.

// markStepNode is a pointer-rich node so the mark phase has real scan
// work to do.
type markStepNode struct {
	next *markStepNode
	prev *markStepNode
	pad  [4]*markStepNode
	buf  [64]byte
}

// buildMarkStepGraph builds a live, scannable graph of roughly totalBytes
// bytes.
func buildMarkStepGraph(totalBytes int) *markStepNode {
	n := totalBytes / int(unsafe.Sizeof(markStepNode{}))
	head := &markStepNode{}
	cur := head
	for i := 0; i < n; i++ {
		nd := &markStepNode{prev: cur}
		cur.next = nd
		if i%3 == 0 {
			nd.pad[0] = head
			nd.pad[1] = cur
		}
		cur = nd
	}
	return head
}

// TestGCMarkStepNoCycle checks that the mark step is a cheap no-op that
// reports no remaining work when no GC cycle is active.
func TestGCMarkStepNoCycle(t *testing.T) {
	// A concurrent background GC (e.g. triggered by another test's
	// allocations in a parallel process is impossible, but within this
	// process another goroutine can trigger one) can legitimately make
	// GCMarkStep return true, so retry a few times: right after a full
	// runtime.GC() there is normally no active cycle.
	for i := 0; i < 100; i++ {
		runtime.GC()
		start := time.Now()
		more := GCMarkStep(1)
		elapsed := time.Since(start)
		if !more {
			if elapsed > time.Second {
				t.Errorf("no-op mark step took %v, want well under 1s", elapsed)
			}
			return
		}
	}
	t.Errorf("GCMarkStep always reported remaining mark work immediately after runtime.GC()")
}

// TestGCMarkStepDrivesMark checks that during a mark phase the mark step
// performs bounded increments of real mark work, reports remaining work
// correctly, and that repeatedly calling it drives the cycle through mark
// termination.
func TestGCMarkStepDrivesMark(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates and scans a large heap")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	// A big live pointer graph makes the mark phase long enough to
	// observe from outside.
	graph := buildMarkStepGraph(32 << 20)
	defer runtime.KeepAlive(graph)
	runtime.GC()

	var memstats runtime.MemStats
	runtime.ReadMemStats(&memstats)
	startNumGC := memstats.NumGC

	var (
		sawWork      bool   // a mark step reported work remaining
		steppedNs    int64  // wall time spent inside true-returning steps
		steppedCalls int64  // number of true-returning steps
		sink         []byte // defeats dead-store elimination
	)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		// Allocate garbage to push the heap toward the trigger.
		for i := 0; i < 100; i++ {
			sink = make([]byte, 4096)
		}
		// Donate a small budget. With GOMAXPROCS=1 and this goroutine
		// hogging the P, mark progress comes from assists and these
		// steps, so during a cycle we must observe work remaining at
		// least once before the cycle can finish.
		start := time.Now()
		more := GCMarkStep(0.5)
		dur := time.Since(start)
		if more {
			sawWork = true
			steppedNs += dur.Nanoseconds()
			steppedCalls++
			// One increment past a 0.5ms budget should be far below
			// this; the bound is deliberately generous for slow,
			// contended CI machines.
			if dur > time.Second {
				t.Fatalf("mark step with 0.5ms budget ran for %v", dur)
			}
		}
		runtime.ReadMemStats(&memstats)
		if sawWork && memstats.NumGC >= startNumGC+2 {
			break
		}
	}
	_ = sink
	if !sawWork {
		t.Fatalf("mark step never reported remaining mark work over %d GC cycles", memstats.NumGC-startNumGC)
	}
	if memstats.NumGC < startNumGC+2 {
		t.Fatalf("GC cycles did not complete while driving marks: NumGC went from %d to %d", startNumGC, memstats.NumGC)
	}
	if steppedCalls > 0 {
		t.Logf("true-returning mark steps: %d, mean duration: %v", steppedCalls, time.Duration(steppedNs/steppedCalls))
	}
}

// TestGCMarkStepZeroBudget checks that a non-positive budget performs no
// drain and just reports whether work remains, quickly.
func TestGCMarkStepZeroBudget(t *testing.T) {
	for i := 0; i < 10; i++ {
		start := time.Now()
		GCMarkStep(0)
		GCMarkStep(-1)
		if d := time.Since(start); d > time.Second {
			t.Fatalf("zero-budget mark steps took %v", d)
		}
	}
}

// TestGCMarkStepConcurrent hammers the mark step from several goroutines
// while another allocates, to exercise the concurrent-caller paths (and
// give the race detector something to chew on). The mark step follows the
// same discipline as concurrent GC assists, so concurrent calls must be
// safe on multi-P platforms.
func TestGCMarkStepConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a large heap")
	}
	graph := buildMarkStepGraph(16 << 20)
	defer runtime.KeepAlive(graph)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				GCMarkStep(0.2)
			}
		}()
	}
	var sink []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for i := 0; i < 1000; i++ {
			sink = make([]byte, 2048)
		}
		runtime.Gosched()
	}
	_ = sink
	close(stop)
	wg.Wait()
	// Make sure the world is still coherent: a full GC must succeed.
	runtime.GC()
}
