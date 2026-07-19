// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime

// GOWASM=threads thread creation (phase B2).
//
// There is no clone/pthread_create on wasm: a "thread" is a host worker
// (node worker_threads) that instantiated the same module against the
// same shared linear memory and then called the wasm_thread_run export
// (sys_wasmthreads.s). Each pool worker sits inside that export in a pure
// -wasm futex wait on the spawn mailbox below - no Go runtime state is
// touched while waiting, so the pool can start before the runtime is
// initialized.
//
// newosproc hands a new M to a waiting worker through the mailbox:
//
//	poster (any M, newosproc):        worker (wasm_thread_run):
//	  acquire mailbox 0 -> 1
//	  wasmSpawnMP = mp
//	  publish     1 -> 2, notify  ->    claim 2 -> 3 (cmpxchg)
//	                                    read wasmSpawnMP
//	                                    release 3 -> 0, notify
//	  wait wasmSpawnSeq change    <-    wasmSpawnSeq++, notify
//	                                    SP = mp.g0.sched.sp
//	                                    g  = mp.g0
//	                                    mstart (never returns)
//
// All fields are ordinary (non-GC-visible) words: mp is kept alive by
// allm. The worker instance has its own wasm globals (SP, g, PAUSE...),
// which is exactly why one instance per worker exists at all.
//
// Pool sizing: wasm_exec_node.js pre-spawns GOWASMTHREADSPOOL workers
// (default 4). Workers currently never return to the pool (Ms never exit
// on wasm - see goexit0's wasm special case), but parked Ms are reused by
// the scheduler (mget), so the pool bounds the number of concurrent Ms,
// not the number of spawns. If no worker claims the M within 10 seconds
// (pool exhausted, pool disabled, or a non-pool host), newosproc throws
// with a pointer at GOWASMTHREADSPOOL. Blocking-with-timeout was chosen
// over failing fast so that a spawn racing the pool's asynchronous
// startup succeeds.

import (
	"internal/runtime/atomic"
	"unsafe"
)

const wasmThreadsEnabled = true

// Spawn mailbox, accessed from Go here and from raw wasm assembly in
// sys_wasmthreads.s (wasm_export_thread_run).
var (
	wasmSpawnState uint32  // 0 free, 1 being written, 2 posted, 3 claimed
	wasmSpawnMP    uintptr // *m being handed off; valid while state is 2/3
	wasmSpawnSeq   uint32  // incremented by a worker on every claim
)

// futexsleep waits on addr (memory.atomic.wait32) while *addr == val, for
// at most ns nanoseconds (ns < 0: no timeout). Implemented in
// sys_wasmthreads.s. Node.js permits this on the main thread as well as
// on workers; browsers do not (main-thread waits throw there).
//
//go:noescape
func futexsleep(addr *uint32, val uint32, ns int64)

// futexwakeup wakes at most cnt waiters blocked on addr
// (memory.atomic.notify). Implemented in sys_wasmthreads.s.
//
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
	// procid is purely diagnostic on wasm; m0 uses 2 (osinit).
	mp.procid = uint64(mp.id) + 2
	// Hand the worker its g0 stack pointer through g0.sched.sp; mstart1
	// re-initializes g0.sched properly once the M is running.
	mp.g0.sched.sp = mp.g0.stack.hi - 16

	// Acquire the mailbox (state 0 -> 1).
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

	// Wait for a worker to claim the M (it bumps wasmSpawnSeq). The
	// timeout catches a missing/exhausted pool; it is generous because
	// the pool workers start asynchronously with the program.
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

// wasmSleep is a futex word that is never woken, used for plain timed
// sleeps (usleep).
var wasmSleep uint32

// wasmThreadsUsleep implements usleep under GOWASM=threads with a timed
// futex wait (without threads, usleep is a no-op busy return; see
// os_js.go).
//
//go:nosplit
func wasmThreadsUsleep(usec uint32) {
	futexsleep(&wasmSleep, 0, int64(usec)*1000)
}

// wasmThreadsCurMID returns the id of the M the calling goroutine is
// running on. Test/demo hook (linknamed by testdata/wasmthreads).
func wasmThreadsCurMID() int64 {
	return getg().m.id
}

// wasmThreadsRunOnNewM runs fn in a new goroutine and guarantees that it
// executes on an M other than the caller's: it privately locks the
// calling goroutine to its M (the public LockOSThread machinery is still
// gated off on wasm in this phase; see dolockOSThread) and then blocks
// until fn's goroutine - which the scheduler must therefore hand, along
// with the P, to another M, creating one via newosproc if none is parked
// - has finished.
//
// This is the phase-B2 test/demo hook for real Go code on worker
// threads (linknamed by testdata/wasmthreads and the runtime tests). fn
// must not use syscall/js or anything built on it (os file I/O, fmt
// printing to os.Stdout...): JavaScript values live on the main thread
// and host calls are not forwarded from worker Ms yet. println (raw
// runtime.wasmWrite) is fine.
func wasmThreadsRunOnNewM(fn func()) {
	gp := getg()
	mp := gp.m

	// Private lockOSThread (dolockOSThread is a no-op on wasm, so set
	// the links directly). lockedInt, not lockedExt: this is a runtime
	// -internal lock, and newm from an lockedExt M would insist on the
	// template thread, which does not exist on wasm (newosproc hands
	// off to identical pool workers, so there is no thread state to
	// inherit and no need for one).
	mp.lockedInt++
	mp.lockedg.set(gp)
	gp.lockedm.set(mp)

	done := make(chan struct{}, 1)
	go func() {
		fn()
		done <- struct{}{}
	}()
	// The receive blocks this goroutine; since it is locked to this M,
	// the scheduler parks this M (stoplockedm) and hands the P to
	// another M (startm -> mget or newosproc), which runs fn's
	// goroutine. When fn is done, the send readies this goroutine and
	// the scheduler hands the P back to this M (startlockedm).
	<-done

	if gp.m != mp {
		throw("wasmThreadsRunOnNewM: locked goroutine migrated Ms")
	}
	mp.lockedInt--
	mp.lockedg = 0
	gp.lockedm = 0
}
