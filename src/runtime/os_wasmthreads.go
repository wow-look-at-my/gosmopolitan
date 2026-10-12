// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime

// GOWASM=threads thread creation (phase B2).

import (
	"internal/runtime/atomic"
	"internal/strconv"
	"unsafe"
)

const wasmThreadsEnabled = true

// Spawn mailbox, accessed from Go here and from raw wasm assembly in
// sys_wasmthreads.s (wasm_export_thread_run).
var (
	wasmSpawnState uint32
	wasmSpawnMP    uintptr
	wasmSpawnSeq   uint32 // incremented by a worker on every claim
)

//go:noescape
func futexsleep(addr *uint32, val uint32, ns int64)

//go:noescape
func futexwakeup(addr *uint32, cnt uint32)

// wasmThreadsNewosproc is newosproc under GOWASM=threads: it posts mp to
// the spawn mailbox and waits until a pool worker claims it.
//
// May run with m.p==nil, so write barriers are not allowed (all stores
// below are scalar).
//
//go:nowritebarrier
func wasmThreadsNewosproc(mp *m) {
	mp.procid = uint64(mp.id) + 2
	// Hand the worker its g0 stack pointer through g0.sched.sp.
	mp.g0.sched.sp = mp.g0.stack.hi - 16

	for {
		v := atomic.Load(&wasmSpawnState)
		if v == 0 {
			if atomic.Cas(&wasmSpawnState, 0, 1) {
				break
			}
			continue
		}
		futexsleep(&wasmSpawnState, v, -1)
	}

	seq := atomic.Load(&wasmSpawnSeq)
	wasmSpawnMP = uintptr(unsafe.Pointer(mp))
	atomic.Store(&wasmSpawnState, 2) // publish
	futexwakeup(&wasmSpawnState, ^uint32(0))

	// Wait for a worker to claim the M (it bumps wasmSpawnSeq).
	deadline := nanotime() + 10e9
	for atomic.Load(&wasmSpawnSeq) == seq {
		now := nanotime()
		if now >= deadline {
			print("runtime: newosproc: no worker thread claimed the new M within 10s\n")
			print("runtime: GOWASM=threads needs the wasm_exec_node.js worker pool (GOWASMTHREADSPOOL > 0, Node.js host)\n")
			throw("newosproc: no wasm worker thread available")
		}
		futexsleep(&wasmSpawnSeq, seq, deadline-now)
	}
}

// wasmSleep is a futex word that is never woken, used for plain timed sleeps (usleep).
var wasmSleep uint32

// wasmSchedNudge is the scheduler nudge word (phase B3).
var wasmSchedNudge uint32

// wasmSchedNudgeWake wakes every M sleeping on wasmSchedNudge (worker Ms in
// beforeIdle's timed idle sleep) and every parked worker M.
//
//go:nosplit
//go:nowritebarrier
func wasmSchedNudgeWake() {
	atomic.Xadd(&wasmSchedNudge, 1)
	futexwakeup(&wasmSchedNudge, ^uint32(0))
	wasmParkWakeAll()
}

// wasmParkWake is the word parked worker Ms sleep on (wasmWorkerParkNote).
var wasmParkWake uint32

// wasmParkWakeAll wakes every parked worker M, to read its note and the
// scheduler state again.
//
//go:nosplit
//go:nowritebarrier
func wasmParkWakeAll() {
	atomic.Xadd(&wasmParkWake, 1)
	futexwakeup(&wasmParkWake, ^uint32(0))
}

// wasmMainWake is the main-thread wake word (phase B3).
var wasmMainWake uint32

//go:nosplit
//go:nowritebarrier
func wasmWakeMainThread() {
	if getg().m == &m0 {
		return
	}
	atomic.Xadd(&wasmMainWake, 1)
	futexwakeup(&wasmMainWake, ^uint32(0))
}

// wasmMainWantsP is set (atomically) by the parked main M when it was resumed by the host (a JavaScript event or timeout) but could not get a P.
var wasmMainWantsP uint32

// wasmThreadsPidleput is called by pidleput (with sched.lock held) when a P
// goes idle under GOWASM=threads.
//
//go:nowritebarrier
func wasmThreadsPidleput(pp *p) {
	if pp.timers.len.Load() > 0 || atomic.Load(&wasmMainWantsP) != 0 || wasmMigrateCount.Load() != 0 {
		wasmWakeMainThread()
	}
}

func wasmPoolSize() int32 {
	env := gogetenv("GOWASMTHREADSPOOL")
	if env == "" {
		return 4
	}
	n, err := strconv.ParseInt(env, 10, 32)
	if err != nil || n < 0 {
		return 4
	}
	return int32(n)
}

// wasmMaxMCount returns the maximum number of Ms this process can ever have.
func wasmMaxMCount() int32 {
	return wasmPoolSize() + 1
}

// wasmClampGOMAXPROCS bounds a requested GOMAXPROCS under GOWASM=threads.
func wasmClampGOMAXPROCS(n int32) int32 {
	if max := wasmPoolSize() + 1; n > max {
		return max
	}
	return n
}

// wasmThreadsUsleep implements usleep under GOWASM=threads with a timed futex
// wait (without threads, usleep is a no-op busy return.
//
//go:nosplit
func wasmThreadsUsleep(usec uint32) {
	futexsleep(&wasmSleep, 0, int64(usec)*1000)
}

//go:wasmimport gojs runtime.wasmMainWakeInit
//go:noescape
func wasmMainWakeInit(addr *uint32)

//go:wasmimport gojs runtime.wasmSetKeepAlive
func wasmSetKeepAlive(on int32)

// wasmThreadsCurMID returns the id of the M the calling goroutine is
// running on. Test/demo hook (linknamed by testdata/wasmthreads).
func wasmThreadsCurMID() int64 {
	return getg().m.id
}

// wasmThreadsIdleWorkerMs returns the number of worker Ms linked on
// sched.midle - parked Ms that a startm can claim via mget instead of asking
// newosproc. For a fresh pool worker.
func wasmThreadsIdleWorkerMs() int32 {
	lock(&sched.lock)
	n := sched.nmidle
	if sched.midle.head() == unsafe.Pointer(&m0) || m0.idleNode.prev != 0 || m0.idleNode.next != 0 {
		// The membership test is wasmMidleRemove's: on the intrusive doubly-linked idle list an M is either the head.
		n--
	}
	unlock(&sched.lock)
	return n
}

// wasmThreadsRunOnNewM runs fn in a new goroutine and guarantees that it
// executes on an M other than the caller's.
func wasmThreadsRunOnNewM(fn func()) {
	gp := getg()
	mp := gp.m

	// Private lockOSThread (dolockOSThread is a no-op on wasm, so set the links directly). lockedInt, not lockedExt.
	mp.lockedInt++
	mp.lockedg.set(gp)
	gp.lockedm.set(mp)

	done := make(chan struct{}, 1)
	go func() {
		fn()
		done <- struct{}{}
	}()
	// The receive blocks this goroutine; since it is locked to this M.
	<-done

	if gp.m != mp {
		throw("wasmThreadsRunOnNewM: locked goroutine migrated Ms")
	}
	mp.lockedInt--
	mp.lockedg = 0
	gp.lockedm = 0
}
