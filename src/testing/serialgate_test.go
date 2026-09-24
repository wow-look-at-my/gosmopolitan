// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"sync"
	"time"
)

// waitFor spins until cond holds, and fails the test rather than hanging when
// it never does. A gate bug shows up as a goroutine that never runs, so a bare
// channel receive here would report itself as a package timeout minutes later
// instead of as this test.
func waitFor(t *T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// A resume must not queue behind a WAITING exclusive caller. This is the whole
// reason serialGate exists rather than a sync.RWMutex, which blocks a new
// reader the moment a writer waits.
//
// The shape is the one that hung the time package: a test holds a shared hold
// and is blocked on something only a second test can release, and that second
// test is coming back from a subtest while a third has asked to run alone.
func TestSerialGateResumeIgnoresAWaitingWriter(t *T) {
	g := newSerialGate()

	// One hold that stays out for the whole test: the reader that, in the real
	// deadlock, was stuck on a lock somebody else owned.
	g.acquire()

	// A Serial caller queues behind it and never gets in while it is held.
	exclusive := make(chan struct{})
	go func() {
		g.acquireExclusive()
		close(exclusive)
	}()
	waitFor(t, "the exclusive caller to queue", func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.waiting == 1
	})

	// The resume is what has to get through. Under an RWMutex it would block
	// here, and everything below would never run.
	resumed := make(chan struct{})
	go func() {
		g.resume()
		close(resumed)
	}()
	select {
	case <-resumed:
	case <-time.After(5 * time.Second):
		t.Fatal("resume blocked behind a waiting exclusive caller")
	}

	select {
	case <-exclusive:
		t.Fatal("the exclusive caller ran while holds were out")
	default:
	}

	// Both holds go, and only then does the Serial caller get the process.
	g.release()
	g.release()
	select {
	case <-exclusive:
	case <-time.After(5 * time.Second):
		t.Fatal("the exclusive caller never ran after the holds were dropped")
	}
	g.releaseExclusive()
}

// A NEW hold does respect a waiting exclusive caller. That is what keeps a
// Serial test from starving while tests keep arriving, and it is the half a
// resume deliberately skips.
func TestSerialGateAcquireYieldsToAWaitingWriter(t *T) {
	g := newSerialGate()
	g.acquire()

	exclusive := make(chan struct{})
	go func() {
		g.acquireExclusive()
		close(exclusive)
	}()
	waitFor(t, "the exclusive caller to queue", func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.waiting == 1
	})

	acquired := make(chan struct{})
	go func() {
		g.acquire()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("a new hold jumped a waiting exclusive caller")
	case <-time.After(100 * time.Millisecond):
	}

	g.release() // the exclusive caller now gets in, ahead of the new hold
	select {
	case <-exclusive:
	case <-time.After(5 * time.Second):
		t.Fatal("the exclusive caller never ran")
	}
	g.releaseExclusive()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("the queued hold never ran after the exclusive caller left")
	}
	g.release()
}

// Exclusive means exclusive: no hold is out while a Serial caller runs.
func TestSerialGateExclusiveExcludes(t *T) {
	g := newSerialGate()
	g.acquireExclusive()

	var wg sync.WaitGroup
	started := make(chan struct{}, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.acquire()
			started <- struct{}{}
			g.release()
		}()
	}

	select {
	case <-started:
		t.Fatal("a hold was handed out while a Serial caller was running")
	case <-time.After(100 * time.Millisecond):
	}

	g.releaseExclusive()
	wg.Wait()
}
