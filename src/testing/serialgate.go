// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import "sync"

// serialGate orders the tests that run at once against a test that asked for
// the process to itself. It is a reader-writer lock with one extra operation,
// and that operation is the reason it is not a sync.RWMutex.
//
// A parent waiting on a subtest drops its shared hold and takes it back
// afterwards (T.Run). Under an RWMutex the second step queues behind any
// writer that arrived in between, because RWMutex blocks a new reader once a
// writer waits. That is the correct rule for a NEW reader and the wrong one
// here: the parent is already running, and it may hold locks of its own that
// another test needs. Three parties then wait in a circle -- the writer for a
// reader to finish, that reader for a lock, and the goroutine holding the lock
// for its place in the reader queue. Nothing breaks it, and the whole package
// times out. The time package did exactly this, four tests deep, for 9m52s.
//
// So resume does not respect a waiting writer, and acquire does. New tests
// still stop arriving the moment a Serial caller asks, which is what keeps the
// writer from starving; a test that already ran gets back in at once, which is
// what keeps it from holding a lock forever behind a queue.
type serialGate struct {
	mu      sync.Mutex
	cond    *sync.Cond
	readers int  // holds handed out
	writer  bool // a Serial test is running alone
	waiting int  // Serial callers queued
}

func newSerialGate() *serialGate {
	g := &serialGate{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// acquire takes a shared hold for a test that is starting. It waits out a
// running Serial test and any that are queued, so a Serial caller reaches the
// front rather than being starved by tests that keep arriving.
func (g *serialGate) acquire() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.writer || g.waiting > 0 {
		g.cond.Wait()
	}
	g.readers++
}

// resume takes back a hold the caller yielded while it waited on a subtest.
// Only a Serial test that is actually RUNNING holds it off. See the type's
// comment for why a queued one must not.
func (g *serialGate) resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.writer {
		g.cond.Wait()
	}
	g.readers++
}

func (g *serialGate) release() {
	g.mu.Lock()
	g.readers--
	if g.readers == 0 {
		g.cond.Broadcast()
	}
	g.mu.Unlock()
}

// acquireExclusive waits until nothing else is running, then hands the process
// to the caller.
func (g *serialGate) acquireExclusive() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waiting++
	for g.writer || g.readers > 0 {
		g.cond.Wait()
	}
	g.waiting--
	g.writer = true
}

func (g *serialGate) releaseExclusive() {
	g.mu.Lock()
	g.writer = false
	g.cond.Broadcast()
	g.mu.Unlock()
}
