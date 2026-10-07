// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import "internal/runtime/atomic"

// CPUProfileAddContended calls cpuprof.add and cpuprof.addNonGo while
// prof.signalLock is held, as a profiling signal finds it when another
// handler or setcpuprofilerate holds it. It returns how many samples
// they counted as dropped.
func CPUProfileAddContended() uint32 {
	if !prof.signalLock.CompareAndSwap(0, 1) {
		panic("prof.signalLock is held")
	}
	before := cpuprof.lostContended.Load()
	stk := []uintptr{1, 2, 3}
	cpuprof.add(nil, stk)
	cpuprof.addNonGo(stk)
	dropped := cpuprof.lostContended.Load() - before
	cpuprof.lostContended.Add(-int32(dropped))
	prof.signalLock.Store(0)
	return dropped
}

// TraceWaitRounds runs rounds of the traceWait protocol between a waiter
// and a releasing goroutine. In each round the releaser sets the
// condition and releases, at whatever point the waiter has reached. The
// waiter sleeps whenever it finds the condition unset, so it returns only
// if no release is lost. The waiter blocks its M with its P, so the
// releaser needs a second P. On hosts that need a sigSafeEvent, signal
// selects the event the trace flush sleeps on there instead of the note.
func TraceWaitRounds(rounds int, signal bool) {
	wait := new(traceWait)
	if signal && sigSafeEventNeeded() {
		if !wait.event.init() {
			panic("sigSafeEvent.init failed")
		}
		wait.useEvent = true
	}
	cond := new(atomic.Uint32)
	next := make(chan uint32)
	finished := make(chan struct{})
	go func() {
		for round := range next {
			cond.Store(round)
			wait.release()
		}
		close(finished)
	}()
	for round := uint32(1); round <= uint32(rounds); round++ {
		next <- round
		for {
			wait.prepare()
			if cond.Load() == round {
				wait.cancel()
				break
			}
			wait.sleep()
		}
	}
	close(next)
	<-finished
}
