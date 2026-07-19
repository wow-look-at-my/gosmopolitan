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
	"internal/strconv"
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

// wasmSchedNudge is the scheduler nudge word (phase B3). Worker Ms doing a
// timed idle sleep in beforeIdle sleep on it, so that a stop-the-world
// request or new work can cut the sleep short instead of waiting out the
// timeout. wasmSchedNudgeWake bumps and notifies it.
var wasmSchedNudge uint32

// wasmSchedNudgeWake wakes every M sleeping on wasmSchedNudge (worker Ms
// in beforeIdle's timed idle sleep). The bump makes a concurrent
// about-to-sleep M's futexsleep return immediately (value mismatch), so a
// wake cannot be lost.
//
//go:nosplit
//go:nowritebarrier
func wasmSchedNudgeWake() {
	atomic.Xadd(&wasmSchedNudge, 1)
	futexwakeup(&wasmSchedNudge, ^uint32(0))
}

// wasmMainWake is the main-thread wake word (phase B3). The JavaScript
// side of the main thread keeps an Atomics.waitAsync armed on it
// (wasmMainWakeInit below); bumping and notifying the word makes the
// host's event loop call resume, waking a main M that is parked in the
// event loop. Node.js >= 16 supports Atomics.waitAsync, and a pending
// waitAsync does not keep the event loop alive, so the exit-time deadlock
// probe still fires.
var wasmMainWake uint32

// wasmWakeMainThread nudges the main thread: if the main M is parked in
// the JavaScript event loop (wasmMainParkNote in lock_jsthreads.go), the
// host resumes it. Callable from any worker thread; spurious nudges from
// workers are cheap (the resumed main M re-checks its state and parks
// again).
//
// Calls on the main thread itself are dropped, and that is load-bearing,
// not just an optimization. The main M is awake right here, and it
// re-checks every wake condition (its park note, the run queues, the
// timers) before it next pauses, so the nudge carries no information.
// But it does have an effect: the host keeps an Atomics.waitAsync
// watcher armed on wasmMainWake ACROSS each resume (wasm_exec.js re-arms
// before calling resume, which is what makes worker wakes race-free), so
// a bump from inside a resume lands on an armed watcher and queues
// another resume as a MICROTASK. JavaScript drains microtasks to
// exhaustion before returning to the macrotask queue. The self-serve
// resume path (wasmMainParkWake -> notewakeup(&m0.park) -> here) would
// therefore re-nudge itself on every iteration: an unbounded microtask
// chain of resumes that starves every macrotask - all JS timers and the
// exit message a worker posts when Go code on it calls runtime.exit -
// observed as multi-second GOMAXPROCS>1 stalls (broken only by a real
// cross-thread wake) and as a permanent hang on the exit path.
//
//go:nosplit
//go:nowritebarrier
func wasmWakeMainThread() {
	if getg().m == &m0 {
		return
	}
	atomic.Xadd(&wasmMainWake, 1)
	futexwakeup(&wasmMainWake, ^uint32(0))
}

// wasmMainWantsP is set (atomically) by the parked main M when it was
// resumed by the host (a JavaScript event or timeout) but could not get a
// P because every P was busy. pidleput checks it: as soon as a P frees,
// the main thread is nudged so it can handle the pending event.
var wasmMainWantsP uint32

// wasmThreadsPidleput is called by pidleput (with sched.lock held) when a
// P goes idle under GOWASM=threads. Two reasons to nudge the main thread:
//
//   - The P still has pending timers. Idle-P timers are backstopped by
//     the main M, which arms a JavaScript timeout for the earliest timer
//     across all Ps before it parks (beforeIdle in lock_jsthreads.go);
//     it must wake to re-arm for this P's timers.
//   - The parked main M wants a P (wasmMainWantsP) to handle a pending
//     JavaScript event.
//
//go:nowritebarrier
func wasmThreadsPidleput(pp *p) {
	if pp.timers.len.Load() > 0 || atomic.Load(&wasmMainWantsP) != 0 {
		wasmWakeMainThread()
	}
}

// wasmPoolSize returns the worker pool size the host was configured with:
// GOWASMTHREADSPOOL, default 4 (mirroring wasm_exec_node.js). It is the
// bound on how many Ms can run concurrently.
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

// wasmMaxMCount returns the maximum number of Ms this process can ever
// have: the worker pool (one M per pool worker thread, claimed for life)
// plus the main thread's m0. startm consults it to avoid demanding an M
// the pool cannot provide.
func wasmMaxMCount() int32 {
	return wasmPoolSize() + 1
}

// wasmClampGOMAXPROCS bounds a requested GOMAXPROCS under GOWASM=threads:
// values above pool+1 (the worker pool plus the main thread) are clamped,
// since Ps beyond the number of threads that can ever run would make the
// scheduler try to start Ms the pool cannot provide (newosproc throws
// after its 10s grace period). Without GOWASM=threads the clamp is 1, as
// before.
func wasmClampGOMAXPROCS(n int32) int32 {
	if max := wasmPoolSize() + 1; n > max {
		return max
	}
	return n
}

// wasmThreadsUsleep implements usleep under GOWASM=threads with a timed
// futex wait (without threads, usleep is a no-op busy return; see
// os_js.go).
//
//go:nosplit
func wasmThreadsUsleep(usec uint32) {
	futexsleep(&wasmSleep, 0, int64(usec)*1000)
}

// wasmMainWakeInit hands the host the address of wasmMainWake. The
// JavaScript side (wasm_exec.js) keeps an Atomics.waitAsync armed on the
// word from then on and calls resume whenever it is notified, which is
// what makes a main M parked in the event loop wakeable from worker
// threads. Called once, on the main thread, before its first park.
//
//go:wasmimport gojs runtime.wasmMainWakeInit
//go:noescape
func wasmMainWakeInit(addr *uint32)

// wasmSetKeepAlive tells the host whether the Go program still has active
// threads while the main M is parked in the event loop. While on (1), the
// host holds its event loop open (worker threads are unref'd and a parked
// main M schedules nothing), so the process cannot exit underneath running
// worker Ms. While off (0), a fully idle program lets the event loop
// drain, which is what triggers the host's exit-time deadlock probe.
//
//go:wasmimport gojs runtime.wasmSetKeepAlive
func wasmSetKeepAlive(on int32)

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
