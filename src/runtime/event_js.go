// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package runtime

import (
	"internal/runtime/sys"
	"unsafe"
)

// This file holds the JavaScript event-loop integration of the js/wasm
// port: pausing to the host when all goroutines are idle, resuming on a
// callback or timeout, and the exit-time deadlock probe. It is shared by
// the default single-threaded runtime (lock_js.go) and the GOWASM=threads
// runtime (lock_jsthreads.go); under threads only the main M - the one
// bound to the host's event loop - ever runs this machinery (see the
// beforeIdle implementations in those files).

// events is a stack of calls from JavaScript into Go.
var events []*event

type event struct {
	// g was the active goroutine when the call from JavaScript occurred.
	// It needs to be active when returning to JavaScript.
	gp *g
	// returned reports whether the event handler has returned.
	// When all goroutines are idle and the event handler has returned,
	// then g gets resumed and returns the execution to JavaScript.
	returned bool
}

type timeoutEvent struct {
	id int32
	// The time when this timeout will be triggered.
	time int64
}

// diff calculates the difference of the event's trigger time and x.
func (e *timeoutEvent) diff(x int64) int64 {
	if e == nil {
		return 0
	}

	diff := x - e.time
	if diff < 0 {
		diff = -diff
	}
	return diff
}

// clear cancels this timeout event.
func (e *timeoutEvent) clear() {
	if e == nil {
		return
	}

	clearTimeoutEvent(e.id)
}

// wasmIdleMarkYieldWakeNs is how soon to wake the runtime after
// throttling an idle mark drain to let the event loop run (see
// wasmIdleMarkYield in mgcmark.go).
const wasmIdleMarkYieldWakeNs = 1e6 // 1ms

// wasmIdleMarkCanYield reports whether skipping idle mark work would let
// the scheduler yield to the JavaScript event loop through the normal
// end-of-entry path: the newest call from JavaScript has finished its Go
// work (returned) and beforeIdle would resume its goroutine, which hands
// control back to JavaScript. If there is no event (e.g. a go:wasmexport
// call is being processed after a stack switch) or the newest event is
// still running Go code, "idle" here must not return to the host, so
// idle mark work should proceed as upstream would.
func wasmIdleMarkCanYield() bool {
	n := len(events)
	return n > 0 && events[n-1].returned
}

// The timeout event started by beforeIdle.
var idleTimeout *timeoutEvent

// The weak timeout event started by beforeIdle for the next periodic forced
// GC when the program has no other wake source (see wasmForceGCCheck in
// proc.go). Weak timeouts do not keep the host's event loop alive, so a
// pending nudge neither prevents the program from exiting nor masks deadlock
// detection.
var idleGCNudge *timeoutEvent

// eventBeforeIdle is the event-loop half of beforeIdle: if we are not
// already handling an event, pause for an async event; if an event handler
// returned, resume it so it can pause the execution. It either returns the
// specific goroutine to schedule next or indicates with otherReady that
// some goroutine became ready. It must only run on the M that is bound to
// the host's JavaScript event loop (always true without GOWASM=threads;
// enforced by beforeIdle in lock_jsthreads.go under threads).
//
// TODO(drchase): need to understand if write barriers are really okay in this context.
//
//go:yeswritebarrierrec
func eventBeforeIdle(now, pollUntil int64) (gp *g, otherReady bool) {
	if wasmIdleMarkYield && gcBlackenEnabled != 0 {
		// Idle marking was throttled so the event loop can run (see
		// gcDrainMarkWorkerIdle). Make sure the runtime is woken again
		// shortly, so the mark phase keeps making progress even if no
		// timer is due.
		if now == 0 {
			now = nanotime()
		}
		if wake := now + wasmIdleMarkYieldWakeNs; pollUntil == 0 || wake < pollUntil {
			pollUntil = wake
		}
	}

	delay := int64(-1)
	if pollUntil != 0 {
		// round up to prevent setTimeout being called early
		delay = (pollUntil-now-1)/1e6 + 1
		if delay > 1e9 {
			// An arbitrary cap on how long to wait for a timer.
			// 1e9 ms == ~11.5 days.
			delay = 1e9
		}
	}

	if delay > 0 && (idleTimeout == nil || idleTimeout.diff(pollUntil) > 1e6) {
		// If the difference is larger than 1 ms, we should reschedule the timeout.
		idleTimeout.clear()

		idleTimeout = &timeoutEvent{
			id:   scheduleTimeoutEvent(delay),
			time: pollUntil,
		}
	}

	if pollUntil == 0 && eventHandler != nil {
		// The program has no timer to wake it, so findRunnable's cap on the
		// idle sleep (see wasmForceGCDeadline in proc.go) does not apply and
		// nothing would wake it for the next periodic forced GC. Arm a weak
		// timeout for that deadline: it resumes the runtime like a regular
		// timeout event, but does not keep the host's event loop alive, so
		// a program that is deadlocked rather than idle still exits and the
		// exit-time deadlock probe still fires. Without an eventHandler
		// (syscall/js not linked) no event can be handled, so no nudge.
		if deadline := wasmForceGCDeadline(); deadline != 0 && (idleGCNudge == nil || idleGCNudge.diff(deadline) > 1e6) {
			idleGCNudge.clear()

			if now == 0 {
				// With no timers, findRunnable's timers.check never
				// computed the current time.
				now = nanotime()
			}
			nudgeDelay := (deadline-now-1)/1e6 + 1 // round up like the timer delay above
			if nudgeDelay < 1 {
				nudgeDelay = 1
			}
			if nudgeDelay > 1e9 {
				nudgeDelay = 1e9
			}
			idleGCNudge = &timeoutEvent{
				id:   scheduleWeakTimeoutEvent(nudgeDelay),
				time: deadline,
			}
		}
	}

	if len(events) == 0 {
		// TODO: this is the line that requires the yeswritebarrierrec
		go handleAsyncEvent()
		return nil, true
	}

	e := events[len(events)-1]
	if e.returned {
		return e.gp, false
	}
	return nil, false
}

var idleStart int64

func handleAsyncEvent() {
	if wasmThreadsEnabled && getg().m != &m0 {
		// pause returns execution to the JavaScript host; only the main
		// M's instance is entered from (and may return to) the event
		// loop. This cannot happen today: eventBeforeIdle only runs on
		// the main M and, with GOMAXPROCS clamped to 1, the M that
		// spawned this goroutine picks it up itself.
		throw("wasm: event-loop pause on non-main M")
	}
	idleStart = nanotime()
	pause(sys.GetCallerSP() - 16)
}

// clearIdleTimeout clears our record of the timeout started by beforeIdle.
func clearIdleTimeout() {
	idleTimeout.clear()
	idleTimeout = nil
}

// clearIdleGCNudge clears our record of the forced-GC nudge started by beforeIdle.
func clearIdleGCNudge() {
	idleGCNudge.clear()
	idleGCNudge = nil
}

// scheduleTimeoutEvent tells the WebAssembly environment to trigger an event after ms milliseconds.
// It returns a timer id that can be used with clearTimeoutEvent.
//
//go:wasmimport gojs runtime.scheduleTimeoutEvent
func scheduleTimeoutEvent(ms int64) int32

// scheduleWeakTimeoutEvent is like scheduleTimeoutEvent, except that the
// scheduled timeout does not keep the host's event loop alive (on Node.js it
// is unref'd; browsers have no such notion). The environment can exit while
// it is pending.
//
//go:wasmimport gojs runtime.scheduleWeakTimeoutEvent
func scheduleWeakTimeoutEvent(ms int64) int32

// clearTimeoutEvent clears a timeout event scheduled by scheduleTimeoutEvent
// or scheduleWeakTimeoutEvent.
//
//go:wasmimport gojs runtime.clearTimeoutEvent
func clearTimeoutEvent(id int32)

// handleEvent gets invoked on a call from JavaScript into Go. It calls the event handler of the syscall/js package
// and then parks the handler goroutine to allow other goroutines to run before giving execution back to JavaScript.
// When no other goroutine is awake any more, beforeIdle resumes the handler goroutine. Now that the same goroutine
// is running as was running when the call came in from JavaScript, execution can be safely passed back to JavaScript.
func handleEvent() {
	if wasmThreadsEnabled && getg().m != &m0 {
		// See handleAsyncEvent: only the main M talks to the event loop.
		throw("wasm: event handler on non-main M")
	}
	if wasmThreadsEnabled && wasmThreadsHandleEventEntry() {
		// GOWASM=threads: the main M was parked in the event loop on its
		// g0 (wasmMainParkNote in lock_jsthreads.go) and this resume
		// woke it. Returning continues the park loop; the pending host
		// event, if any, is handled once the main M has a P again (the
		// event goroutine spawned by beforeIdle).
		return
	}

	sched.idleTime.Add(nanotime() - idleStart)

	// The event loop just ran; idle marking may resume.
	wasmIdleMarkYield = false

	e := &event{
		gp:       getg(),
		returned: false,
	}
	events = append(events, e)

	if eventHandler == nil {
		// The program does not link in syscall/js, so setEventHandler was
		// never called and this event cannot be handled. The only source of
		// such an event is the host probing for a deadlock: wasm_exec_node.js
		// resumes the program with a synthetic event when Node.js's event
		// loop runs dry while the program is still running. Park this
		// goroutine forever with the event unreturned: beforeIdle then cannot
		// return to JavaScript, so the scheduler falls through to checkdead
		// and reports the deadlock, instead of crashing on a nil eventHandler
		// call. See go.dev/issue/70869.
		//
		// Since this event is the deadlock probe, record that so that
		// deadlockOSHint does not misattribute the deadlock to a blocked
		// js.FuncOf callback (no such callback can exist without syscall/js).
		deadlockProbeActive = true
		clearIdleTimeout()
		gopark(nil, nil, waitReasonZero, traceBlockGeneric, 1)
		throw("unreachable") // gopark above never returns
	}

	if wasmThreadsEnabled {
		// GOWASM=threads: this is a synchronous nested event (JavaScript
		// called into Go from a JavaScript call Go made; asynchronous
		// resumes of a parked main M go through wasmEventGoroutine, see
		// wasmThreadsHandleEventEntry). Run the handler on this
		// goroutine, locked to the main M so a handler that blocks and
		// is readied from a worker thread still continues here (the
		// events bookkeeping and the final pause are main-thread-only);
		// then pause straight back to the synchronously waiting
		// JavaScript caller. There is no wait-for-idle: under threads,
		// returning to JavaScript does not stop the world, and this
		// goroutine continues - on the main M - when the host's call
		// into Go returns.
		gp := getg()
		mp := gp.m
		if mp.lockedg != 0 && mp.lockedg.ptr() != gp {
			throw("wasm: nested event on locked M")
		}
		mp.lockedInt++
		mp.lockedg.set(gp)
		gp.lockedm.set(mp)

		if !eventHandler() {
			// A timeout fired rather than a window event; clear both
			// timeout records (each is re-armed by beforeIdle if still
			// needed).
			clearIdleTimeout()
			clearIdleGCNudge()
		}

		// Synchronous events are strictly LIFO on this thread, and no
		// asynchronous event goroutine can be created while this M is
		// locked, so e is the top of the stack.
		events[len(events)-1] = nil
		events = events[:len(events)-1]

		mp.lockedInt--
		if mp.lockedInt == 0 {
			mp.lockedg = 0
			gp.lockedm = 0
		}

		// return execution to JavaScript
		idleStart = nanotime()
		pause(sys.GetCallerSP() - 16)
		throw("unreachable") // pause above discards this frame
	}

	if !eventHandler() {
		// If we did not handle a window event, a timeout (the idle timeout
		// or the forced-GC nudge) was triggered, so we can clear it. We
		// cannot tell which one fired; clearing both is safe, since each is
		// re-armed by beforeIdle on the next idle pass if still needed.
		clearIdleTimeout()
		clearIdleGCNudge()
	}

	// wait until all goroutines are idle
	e.returned = true
	gopark(nil, nil, waitReasonZero, traceBlockGeneric, 1)

	events[len(events)-1] = nil
	events = events[:len(events)-1]

	// return execution to JavaScript
	idleStart = nanotime()
	pause(sys.GetCallerSP() - 16)
}

// eventHandler retrieves and executes handlers for pending JavaScript events.
// It returns true if an event was handled.
var eventHandler func() bool

//go:linkname setEventHandler syscall/js.setEventHandler
func setEventHandler(fn func() bool) {
	eventHandler = fn
}

// deadlockProbeActive reports whether the JavaScript environment injected its
// exit-time deadlock probe (the pending event with id 0, see wasm_exec_node.js).
// The probe's handler parks itself inside an event, so it must be discounted
// when deciding whether a user callback is blocked.
var deadlockProbeActive bool

// deadlockProbe is called by syscall/js.handleEvent when it receives the
// environment's deadlock probe event (id 0).
//
//go:linkname deadlockProbe syscall/js.deadlockProbe
func deadlockProbe() {
	deadlockProbeActive = true
}

// eventLoopCanWake reports whether a JavaScript event could still wake
// this program: syscall/js is linked (so the host can deliver events into
// Go) and the host's exit-time deadlock probe has not fired yet. Consulted
// by checkdead under GOWASM=threads, where an idle main M parks through
// stopm and would otherwise trip the deadlock detector on a program that
// is merely waiting for a JavaScript event. Once the host's event loop
// runs dry it injects the deadlock probe (deadlockProbeActive), after
// which nothing external can wake the program and checkdead reports the
// deadlock.
func eventLoopCanWake() bool {
	return eventHandler != nil && !deadlockProbeActive
}

// wasmThreadsOnWorker reports whether the calling goroutine is running on
// a worker-thread M under GOWASM=threads. syscall/js links to it to give
// a clear panic when JavaScript values are touched off the main thread
// (JavaScript values and the event loop live on the main thread only;
// worker-thread routing is a later phase). Always false without
// GOWASM=threads.
//
//go:linkname wasmThreadsOnWorker syscall/js.runtimeOnWorkerThread
func wasmThreadsOnWorker() bool {
	return wasmThreadsEnabled && getg().m != &m0
}

// wasmThreadsBuildEnabled reports whether this program was built with
// GOWASM=threads (a build-time constant). syscall/js links to it where a
// per-call runtimeOnWorkerThread check would be a TOCTOU: a preemptible
// goroutine can migrate between Ms between the check and the host call
// (see the Value finalizer in syscall/js).
//
//go:linkname wasmThreadsBuildEnabled syscall/js.runtimeThreadsEnabled
func wasmThreadsBuildEnabled() bool {
	return wasmThreadsEnabled
}

// wasmThreadsBeginMainOp marks the calling goroutine as main-thread-only
// for the duration of a syscall/js operation (a nesting counter, so the
// package's operations may call each other). While marked, schedule() on
// a worker M refuses to execute the goroutine and hands it to the
// migrate queue instead (see wasmSchedPushMainOnly in proc.go) - closing
// the TOCTOU between "confirmed on the main thread" and the host import
// call(s): without it, any preemption or park in that window (loop-gate
// yield, stack-growth preempt, GC assist park) could reschedule the
// goroutine onto a worker M, whose instance stubs every syscall/js
// import with a throw. Unlike an m.locks pin, the mark keeps the
// goroutine fully preemptible and parkable - it only constrains WHERE it
// resumes.
//
// Note the mark does not teleport: if the caller is currently on a
// worker M it stays there until the next reschedule, so syscall/js sets
// the mark FIRST and then checks/migrates - after that check, every
// resume point is the main M.
//
//go:linkname wasmThreadsBeginMainOp syscall/js.runtimeBeginMainOp
func wasmThreadsBeginMainOp() {
	gp := getg()
	if gp.wasmMainOnly == ^uint8(0) {
		throw("wasm: syscall/js main-thread operations nested too deeply")
	}
	gp.wasmMainOnly++
}

// wasmThreadsEndMainOp closes a wasmThreadsBeginMainOp region.
//
//go:linkname wasmThreadsEndMainOp syscall/js.runtimeEndMainOp
func wasmThreadsEndMainOp() {
	gp := getg()
	if gp.wasmMainOnly == 0 {
		throw("wasm: unbalanced syscall/js main-op end")
	}
	gp.wasmMainOnly--
}

// wasmThreadsMigrateToMain moves the calling goroutine to the main M
// under GOWASM=threads: it parks and publishes itself on the migrate
// queue (see wasmSchedPickMigrated in proc.go), which only the main M's
// scheduler pops - the goroutine is executed there directly and never
// enters a run queue. syscall/js links to it so a JavaScript operation
// from a goroutine that landed on a worker M continues on the main
// thread instead of failing.
//
// Returns false without parking when migration cannot succeed: the main
// M is exclusively bound to a different locked goroutine (a blocked
// event handler, or a RunOnNewM hook call from the main goroutine), so
// it could never pick us up; the caller should fail loudly rather than
// deadlock. The lockedg read is racy, but a false negative merely turns
// a wait into a clear panic.
//
//go:linkname wasmThreadsMigrateToMain syscall/js.runtimeMigrateToMain
func wasmThreadsMigrateToMain() bool {
	gp := getg()
	if gp.m == &m0 {
		return true
	}
	if lockedg := m0.lockedg.ptr(); lockedg != nil && lockedg != gp {
		return false
	}
	gopark(wasmMigrateParkFn, nil, waitReasonZero, traceBlockGeneric, 1)
	if getg().m != &m0 {
		throw("wasm: migrated goroutine resumed off the main M")
	}
	return true
}

// wasmMigrateParkFn publishes the parked goroutine on the migrate queue
// and pokes the main M: a host nudge if it is parked in the event loop,
// and the loop-preemption checks of whatever it is running. Runs on the
// worker M's g0 after gp is in _Gwaiting, so the main M cannot observe a
// running goroutine.
func wasmMigrateParkFn(gp *g, _ unsafe.Pointer) bool {
	lock(&wasmMigrateLock)
	wasmMigrateQ.push(gp)
	wasmMigrateCount.Add(1)
	unlock(&wasmMigrateLock)
	wasmWakeMainThread()
	if mgp := m0.curg; mgp != nil {
		mgp.stackguard1 = stackPreempt
	}
	// A worker M idling in beforeIdle's timed sleep may be holding the P
	// the main M needs to run this goroutine; wake it so it re-checks
	// (and releases the P - see the migrate/wantsP bail in beforeIdle).
	wasmSchedNudgeWake()
	return true
}

// deadlockOSHint prints js-specific context just before checkdead's
// "all goroutines are asleep" fatal error.
func deadlockOSHint() {
	n := len(events)
	if deadlockProbeActive {
		// The environment's deadlock probe parks inside an event of its
		// own; it is not a user callback.
		n--
	}
	if n > 0 {
		print("runtime: note: a goroutine is blocked in a call from JavaScript (js.FuncOf callback) that has not returned\n")
		print("runtime: the JavaScript event loop cannot run until the callback returns, so no JavaScript event or callback can unblock it\n")
	}
}
