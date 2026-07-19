// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package runtime

import (
	"internal/runtime/sys"
	_ "unsafe" // for go:linkname
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
