// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package testing

import "sync"

// serialGate orders the tests that run at once against a test that asked for
// the process to itself.
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

// acquire takes a shared hold for a test that is starting.
func (g *serialGate) acquire() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.writer || g.waiting > 0 {
		g.cond.Wait()
	}
	g.readers++
}

// resume takes back a hold the caller yielded while it waited on a subtest.
// Only a Serial test that is RUNNING holds it off.
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
