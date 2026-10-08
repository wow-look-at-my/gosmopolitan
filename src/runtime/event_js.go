// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build js && wasm

package runtime

import (
	"internal/runtime/sys"
	"unsafe"
)

// This file holds the JavaScript event-loop integration of the js/wasm port: pausing to the host when all goroutines are idle.

// events is a stack of calls from JavaScript into Go.
var events []*event

type event struct {
	// g was the active goroutine when the call from JavaScript occurred.
	gp *g
	// returned reports whether the event handler has returned.
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

// wasmIdleMarkYieldWakeNs is how soon to wake the runtime after throttling an idle mark drain to let the event loop run.
const wasmIdleMarkYieldWakeNs = 1e6 // 1ms

// wasmIdleMarkCanYield reports whether skipping idle mark work would let the
// scheduler yield.
func wasmIdleMarkCanYield() bool {
	n := len(events)
	return n > 0 && events[n-1].returned
}

// The timeout event started by beforeIdle.
var idleTimeout *timeoutEvent

// The weak timeout event started by beforeIdle for the next periodic forced GC when the program has no other wake source.
var idleGCNudge *timeoutEvent

// eventBeforeIdle is the event-loop half of beforeIdle: if we are not already
// handling an event, pause for an async event. This also covers if an event
// handler returned, resume it so it can pause the execution. It either
// returns the specific goroutine to schedule next or indicates with
// otherReady that some goroutine became ready. It must only run on the M that
// is bound to the host's JavaScript event loop (always true without
// GOWASM=threads; enforced by beforeIdle in lock_jsthreads.go under threads).
//
// TODO(drchase): need to understand if write barriers are okay in this
// context.
//
//go:yeswritebarrierrec
func eventBeforeIdle(now, pollUntil int64) (gp *g, otherReady bool) {
	if wasmIdleMarkYield && gcBlackenEnabled != 0 {
		// Idle marking was throttled so the event loop can run (see
		// gcDrainMarkWorkerIdle).
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
			delay = 1e9
		}
	}

	if delay > 0 && (idleTimeout == nil || idleTimeout.diff(pollUntil) > 1e6) {
		// If the difference is larger than multiple ms, we should reschedule the timeout.
		idleTimeout.clear()

		idleTimeout = &timeoutEvent{
			id:   scheduleTimeoutEvent(delay),
			time: pollUntil,
		}
	}

	if pollUntil == 0 && eventHandler != nil {
		// The program has no timer to wake it, so findRunnable's cap on the idle
		// sleep (see wasmForceGCDeadline in proc.go) does not apply and nothing
		// would wake it. For the next periodic forced GC.
		if deadline := wasmForceGCDeadline(); deadline != 0 && (idleGCNudge == nil || idleGCNudge.diff(deadline) > 1e6) {
			idleGCNudge.clear()

			if now == 0 {
				// With no timers, findRunnable's timers.check never computed the current time.
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
		// pause returns execution to the JavaScript host.
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

//go:wasmimport gojs runtime.scheduleTimeoutEvent
func scheduleTimeoutEvent(ms int64) int32

//go:wasmimport gojs runtime.scheduleWeakTimeoutEvent
func scheduleWeakTimeoutEvent(ms int64) int32

//go:wasmimport gojs runtime.clearTimeoutEvent
func clearTimeoutEvent(id int32)

// handleEvent gets invoked on a call from JavaScript into Go. It calls the event handler of the syscall/js package.
// It then parks the handler goroutine to allow other goroutines to run before giving execution back to JavaScript.
// When no other goroutine is awake any more, beforeIdle resumes the handler goroutine. Now that the same goroutine is
// running as was running when the call came in from JavaScript, execution can be safely passed back to JavaScript.
func handleEvent() {
	if wasmThreadsEnabled && getg().m != &m0 {
		// See handleAsyncEvent: only the main M talks to the event loop.
		throw("wasm: event handler on non-main M")
	}
	if wasmThreadsEnabled && wasmThreadsHandleEventEntry() {
		// GOWASM=threads: the main M was parked in the event loop on its g0 (wasmMainParkNote in lock_jsthreads.go).
		return
	}

	if !wasmThreadsEnabled {
		// The M held its (only) P across the event-loop pause, so the limiter-event machinery never saw that idleness.
		sched.idleTime.Add(nanotime() - idleStart)
	}

	// The event loop ran; idle marking may resume.
	wasmIdleMarkYield = false

	e := &event{
		gp:       getg(),
		returned: false,
	}
	events = append(events, e)

	if eventHandler == nil {
		// The program does not link in syscall/js, so setEventHandler was never called and this event cannot be handled.
		deadlockProbeActive = true
		clearIdleTimeout()
		gopark(nil, nil, waitReasonZero, traceBlockGeneric, 1)
		throw("unreachable") // gopark above never returns
	}

	if wasmThreadsEnabled {
		// GOWASM=threads: this is a synchronous nested event.
		gp := getg()
		mp := gp.m
		if mp.lockedg != 0 && mp.lockedg.ptr() != gp {
			throw("wasm: nested event on locked M")
		}
		mp.lockedInt++
		mp.lockedg.set(gp)
		gp.lockedm.set(mp)

		if !eventHandler() {
			// A timeout fired rather than a window event.
			clearIdleTimeout()
			clearIdleGCNudge()
		}

		// Synchronous events are strictly LIFO on this thread.
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
		// If we did not handle a window event, a timeout (the idle timeout or the forced-GC nudge) was triggered.
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

// eventHandler retrieves and executes handlers for pending JavaScript events. It returns true if an event was handled.
var eventHandler func() bool

//go:linkname setEventHandler syscall/js.setEventHandler
func setEventHandler(fn func() bool) {
	eventHandler = fn
}

// deadlockProbeActive reports whether the JavaScript environment injected its exit-time deadlock probe.
var deadlockProbeActive bool

//
//go:linkname deadlockProbe syscall/js.deadlockProbe
func deadlockProbe() {
	deadlockProbeActive = true
}

// eventLoopCanWake reports whether a JavaScript event can still wake this
// program.
func eventLoopCanWake() bool {
	return eventHandler != nil && !deadlockProbeActive
}

//go:linkname wasmThreadsOnWorker syscall/js.runtimeOnWorkerThread
func wasmThreadsOnWorker() bool {
	return wasmThreadsEnabled && getg().m != &m0
}

//go:linkname wasmThreadsBuildEnabled syscall/js.runtimeThreadsEnabled
func wasmThreadsBuildEnabled() bool {
	return wasmThreadsEnabled
}

// wasmThreadsBeginMainOp marks the calling goroutine as main-thread-only for
// the duration of a syscall/js operation.
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

// wasmThreadsMigrateToMain moves the calling goroutine to the main M under
// GOWASM=threads: it parks. It publishes itself on the migrate queue (see
// wasmSchedPickMigrated in proc.go), which only the main M's scheduler pops -
// the goroutine is executed there directly. It never enters a run queue.
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

// wasmMigrateParkFn publishes the parked goroutine on the migrate queue. It
// pokes the main M. A host nudge if it is parked in the event loop, and the
// loop-preemption checks of whatever it is running.
func wasmMigrateParkFn(gp *g, _ unsafe.Pointer) bool {
	lock(&wasmMigrateLock)
	wasmMigrateQ.push(gp)
	wasmMigrateCount.Add(1)
	unlock(&wasmMigrateLock)
	wasmWakeMainThread()
	if mgp := m0.curg; mgp != nil {
		mgp.stackguard1 = stackPreempt
	}
	// A worker M idling in beforeIdle's timed sleep may be holding the P the main M needs to run this goroutine.
	wasmSchedNudgeWake()
	return true
}

// deadlockOSHint prints js-specific context before checkdead's "all
// goroutines are asleep" fatal error.
func deadlockOSHint() {
	n := len(events)
	if deadlockProbeActive {
		// The environment's deadlock probe parks inside an event of its own; it is not a user callback.
		n--
	}
	if n > 0 {
		print("runtime: note: a goroutine is blocked in a call from JavaScript (js.FuncOf callback) that has not returned\n")
		print("runtime: the JavaScript event loop cannot run until the callback returns, so no JavaScript event or callback can unblock it\n")
	}
}
