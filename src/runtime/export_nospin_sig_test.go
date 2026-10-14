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

// SignalNoteRounds runs rounds of the signalNote protocol between a waiter
// and a waking goroutine. In each round the waker sets the condition and
// wakes, at whatever point the waiter has reached. The waiter sleeps
// whenever it finds the condition unset, so it returns only if no wakeup
// is lost. The waiter blocks its M with its P, so the waker runs on another
// P. With timeout > 0 the waiter sleeps with sleepFor and that timeout,
// and disarms after each timeout, as the crash relay does.
func SignalNoteRounds(rounds int, timeout int64) {
	note := new(signalNote)
	cond := new(atomic.Uint32)
	// The waker watches want on a P of its own. A channel send would ready
	// it on the waiter's P, which the waiter then blocks, so it would run
	// only if another P stole it.
	want := new(atomic.Uint32)
	alive := new(atomic.Bool)
	const stop = ^uint32(0)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		alive.Store(true)
		for seen := uint32(0); ; {
			round := want.Load()
			if round == stop {
				return
			}
			if round == seen {
				Gosched()
				continue
			}
			seen = round
			cond.Store(round)
			note.wake()
		}
	}()
	defer func() {
		want.Store(stop)
		<-finished
	}()
	// The new waker starts on this P. Gosched hands it the P until it runs.
	for !alive.Load() {
		Gosched()
	}
	for round := uint32(1); round <= uint32(rounds); round++ {
		want.Store(round)
		systemstack(func() {
			for {
				note.arm()
				if cond.Load() == round {
					note.disarm()
					return
				}
				if timeout <= 0 {
					note.sleep()
					continue
				}
				if !note.sleepFor(timeout) {
					note.disarm()
				}
			}
		})
	}
}
