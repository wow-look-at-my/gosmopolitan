// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime

// GOWASM=threads runtime locks: futex-style mutexes and notes over the
// wasm threads proposal's memory.atomic.wait32/notify instructions
// (futexsleep/futexwakeup in sys_wasmthreads.s). This is what makes Ms on
// different worker threads able to block and wake each other.
//
// Main-thread caveat (phase B2): the main M may futex-wait too. Node.js
// permits Atomics-style waits on the main thread, so this is sound under
// node, but while the main M is blocked the host event loop is stalled:
// no JavaScript timers or callbacks run until it wakes (a worker M wakes
// it via futexwakeup). Browsers forbid main-thread waits entirely, so
// GOWASM=threads with worker Ms is node-only in this phase. A fully
// non-blocking main-thread park is a later phase (B3).
//
// The event-loop machinery itself (event_js.go) only ever runs on the
// main M: beforeIdle below routes worker Ms to futex parking instead.

import (
	"internal/runtime/atomic"
	"internal/runtime/sys"
	"unsafe"
)

const (
	mutex_unlocked = 0
	mutex_locked   = 1
	mutex_sleeping = 2

	active_spin     = 4
	active_spin_cnt = 30
	passive_spin    = 1
)

// We use the uintptr mutex.key and note.key as a uint32.
//
//go:nosplit
func key32(p *uintptr) *uint32 {
	return (*uint32)(unsafe.Pointer(p))
}

type mWaitList struct{}

func lockVerifyMSize() {}

func mutexContended(l *mutex) bool {
	return atomic.Load(key32(&l.key)) > mutex_locked
}

func lock(l *mutex) {
	lockWithRank(l, getLockRank(l))
}

// This is the classic futex mutex (see lock_futex.go's ancestry):
// possible lock states are mutex_unlocked, mutex_locked and
// mutex_sleeping. mutex_sleeping means that there is presumably at
// least one sleeping thread.
func lock2(l *mutex) {
	gp := getg()

	if gp.m.locks < 0 {
		throw("runtime·lock: lock count")
	}
	gp.m.locks++

	// Speculative grab for lock.
	v := atomic.Xchg(key32(&l.key), mutex_locked)
	if v == mutex_unlocked {
		return
	}

	// wait is either MUTEX_LOCKED or MUTEX_SLEEPING
	// depending on whether there is a thread sleeping
	// on this mutex. If we ever change l->key from
	// MUTEX_SLEEPING to some other value, we must be
	// careful to change it back to MUTEX_SLEEPING before
	// returning, to ensure that the sleeping thread gets
	// its wakeup call.
	wait := v

	// On a uniprocessor there is no point spinning: the owner cannot
	// make progress while we spin. numCPUStartup is 1 on wasm today,
	// so go straight to sleeping.
	spin := 0
	if numCPUStartup > 1 {
		spin = active_spin
	}
	for {
		// Try for lock, spinning.
		for i := 0; i < spin; i++ {
			for l.key == mutex_unlocked {
				if atomic.Cas(key32(&l.key), mutex_unlocked, wait) {
					return
				}
			}
			procyield(active_spin_cnt)
		}

		// Try for lock, rescheduling.
		for i := 0; i < passive_spin; i++ {
			for l.key == mutex_unlocked {
				if atomic.Cas(key32(&l.key), mutex_unlocked, wait) {
					return
				}
			}
			osyield()
		}

		// Sleep.
		v = atomic.Xchg(key32(&l.key), mutex_sleeping)
		if v == mutex_unlocked {
			return
		}
		wait = mutex_sleeping
		gp.m.blocked = true
		futexsleep(key32(&l.key), mutex_sleeping, -1)
		gp.m.blocked = false
	}
}

func unlock(l *mutex) {
	unlockWithRank(l)
}

func unlock2(l *mutex) {
	v := atomic.Xchg(key32(&l.key), mutex_unlocked)
	if v == mutex_unlocked {
		throw("unlock of unlocked lock")
	}
	if v == mutex_sleeping {
		futexwakeup(key32(&l.key), 1)
	}

	gp := getg()
	gp.m.locks--
	if gp.m.locks < 0 {
		throw("runtime·unlock: lock count")
	}
	if gp.m.locks == 0 && gp.preempt { // restore the preemption request in case we've cleared it in newstack
		gp.stackguard0 = stackPreempt
	}
}

// One-time notifications.

// noteGLock protects the gp field of all notes (the goroutine parked by
// notetsleepg(n, -1)).
var noteGLock mutex

func noteclear(n *note) {
	n.key = 0
	n.gp = 0
}

func notewakeup(n *note) {
	old := atomic.Xchg(key32(&n.key), 1)
	if old != 0 {
		print("notewakeup - double wakeup (", old, ")\n")
		throw("notewakeup - double wakeup")
	}
	// Wake an M blocked in notesleep/notetsleep.
	futexwakeup(key32(&n.key), 1)
	if n == &m0.park {
		// The main M parks its park note in the JavaScript event loop
		// (wasmMainParkNote), not in a futex wait; nudge it through the
		// host so it observes the wakeup.
		wasmWakeMainThread()
	}
	// Wake a goroutine parked by notetsleepg(n, -1), if any.
	lock(&noteGLock)
	gp := n.gp.ptr()
	n.gp = 0
	unlock(&noteGLock)
	if gp != nil {
		goready(gp, 1)
	}
}

func notesleep(n *note) {
	gp := getg()
	if gp != gp.m.g0 {
		throw("notesleep not on g0")
	}
	if n == &gp.m.park {
		if gp.m == &m0 {
			// The main M's park note (stopm/mPark, stoplockedm): park in
			// the host's JavaScript event loop instead of blocking the
			// thread in a futex wait, so timers and events keep being
			// delivered while the main M is idle. See wasmMainParkNote.
			wasmMainParkNote(n)
			return
		}
		// A worker M's park note: a timed sleep that backstops idle-P
		// timers. See wasmWorkerParkNote.
		wasmWorkerParkNote(n)
		return
	}
	for atomic.Load(key32(&n.key)) == 0 {
		gp.m.blocked = true
		futexsleep(key32(&n.key), 0, -1)
		gp.m.blocked = false
	}
}

// wasmWorkerParkNote parks a worker M on its park note (stopm). The main
// M's JavaScript timeout backstops idle-P timers only while the main M is
// parked in the event loop; it can be blocked elsewhere (a timed note
// sleep, a long-running goroutine). So parked worker Ms sleep with a
// timeout when timers exist anywhere: on expiry, a worker whose timers
// are due re-enters the scheduler on its own (remove itself from the idle
// M list, take an idle P, self-complete the stopm protocol), and
// findRunnable's timer checks fire the timers.
func wasmWorkerParkNote(n *note) {
	gp := getg()
	wasmParkedWorkers.Add(1)
	for atomic.Load(key32(&n.key)) == 0 {
		// Always sleep with a timeout: parked workers double as the
		// port's sysmon substitute. On expiry they self-serve pending
		// timers AND act as a watchdog for runnable work that lost its
		// wakeup race (sysmon fills that role on other platforms; wasm
		// has none).
		ns := int64(250e6)
		if next := wasmEarliestTimerWake(); next != 0 {
			if d := next - nanotime(); d < ns {
				ns = d
			}
			if ns < 1e5 {
				ns = 1e5 // floor: don't busy-spin on an overdue timer we may not win
			}
		}
		gp.m.blocked = true
		futexsleep(key32(&n.key), 0, ns)
		gp.m.blocked = false
		if atomic.Load(key32(&n.key)) != 0 {
			wasmParkedWorkers.Add(-1)
			return // real wakeup: nextp installed by the waker
		}
		// Timed out (or spurious): anything to do?
		if wasmMigrateCount.Load() != 0 {
			// Goroutines are waiting to migrate to the main M, and only
			// its findRunnable can pop them. The push-time nudge is
			// single-shot (a resume that could not take a P, or that
			// landed while the main M was mid-transition, consumes it
			// without serving the queue), so parked workers re-deliver
			// it: at most one nudge per watchdog tick per worker, each
			// causing one main-thread resume - bounded, no microtask
			// chain (see wasmWakeMainThread).
			wasmWakeMainThread()
		}
		due := false
		if next := wasmEarliestTimerWake(); next != 0 && nanotime() >= next {
			due = true
		}
		if !due && sched.runq.size == 0 && sched.npidle.Load() == int32(gomaxprocs) {
			// No due timers, nothing in the global queue, and every P is
			// idle (an idle P's local queue is empty by invariant):
			// nothing a scheduler pass could find.
			continue
		}
		// Become a scheduler M again if an idle P is available;
		// findRunnable then runs timers and queued work.
		lock(&sched.lock)
		if !wasmMidleRemove(gp.m) {
			// A concurrent startm claimed us; it will wake the note.
			unlock(&sched.lock)
			continue
		}
		pp, _ := pidleget(0)
		if pp == nil {
			// Every P busy: re-arm the running goroutines' preemption
			// checks so an owner yields and finds the due work.
			mput(gp.m)
			wasmThreadsKick()
			unlock(&sched.lock)
			continue
		}
		gp.m.nextp.set(pp)
		unlock(&sched.lock)
		// Self-complete the stopm protocol; the loop exits above.
		notewakeup(n)
	}
	wasmParkedWorkers.Add(-1)
}

// May run with m.p==nil if called from notetsleep, so write barriers
// are not allowed.
//
//go:nosplit
//go:nowritebarrier
func notetsleep_internal(n *note, ns int64) bool {
	gp := getg()

	if ns < 0 {
		for atomic.Load(key32(&n.key)) == 0 {
			gp.m.blocked = true
			futexsleep(key32(&n.key), 0, -1)
			gp.m.blocked = false
		}
		return true
	}

	if atomic.Load(key32(&n.key)) != 0 {
		return true
	}

	deadline := nanotime() + ns
	for {
		gp.m.blocked = true
		futexsleep(key32(&n.key), 0, ns)
		gp.m.blocked = false
		if atomic.Load(key32(&n.key)) != 0 {
			break
		}
		now := nanotime()
		if now >= deadline {
			break
		}
		ns = deadline - now
	}
	return atomic.Load(key32(&n.key)) != 0
}

func notetsleep(n *note, ns int64) bool {
	gp := getg()
	if gp != gp.m.g0 && gp.m.preemptoff != "" {
		throw("notetsleep not on g0")
	}

	return notetsleep_internal(n, ns)
}

// same as runtime·notetsleep, but called on user g (not g0)
func notetsleepg(n *note, ns int64) bool {
	gp := getg()
	if gp == gp.m.g0 {
		throw("notetsleepg on g0")
	}

	if ns < 0 {
		// Park the goroutine instead of blocking the M. An untimed
		// notetsleepg can pend for the program's whole lifetime (e.g.
		// os/signal's loop, profile readers); on js the M it happens
		// to run on may be the main M, whose thread drives the host
		// event loop and must not block indefinitely.
		for atomic.Load(key32(&n.key)) == 0 {
			gopark(notetsleepgPark, unsafe.Pointer(n), waitReasonZero, traceBlockGeneric, 1)
		}
		return true
	}

	// Timed waits keep the futex path (rare in the js port). This blocks
	// the M until the note is woken or the timeout elapses.
	entersyscallblock()
	ok := notetsleep_internal(n, ns)
	exitsyscall()
	return ok
}

// notetsleepgPark is the gopark callback of notetsleepg: it publishes the
// parked g on the note under noteGLock, unless the note was woken in the
// meantime (then the park is abandoned).
func notetsleepgPark(gp *g, np unsafe.Pointer) bool {
	n := (*note)(np)
	lock(&noteGLock)
	if atomic.Load(key32(&n.key)) != 0 {
		unlock(&noteGLock)
		return false // already woken, do not park
	}
	if n.gp != 0 {
		unlock(&noteGLock)
		throw("notetsleepg - note already has a waiting g")
	}
	n.gp.set(gp)
	unlock(&noteGLock)
	return true
}

// checkTimeouts is a no-op under GOWASM=threads: the deadline-note list it
// scans without GOWASM=threads (see lock_js.go) does not exist here, since
// notetsleepg uses futex timeouts instead of event-loop timeouts.
func checkTimeouts() {}

// Main-thread event-loop park (phase B3).
//
// When the main M goes idle it takes the ordinary scheduler path: it
// releases its P (findRunnable: pidleput) and parks in stopm -> mPark ->
// notesleep(&m0.park). The notesleep special case above routes it here:
// instead of blocking the thread in a futex wait - which would freeze the
// host's JavaScript event loop - the main M returns to the event loop with
// pause, after arming the machinery that can wake it:
//
//   - JavaScript events and timeouts resume it as always (handleEvent).
//   - Other threads nudge it through wasmMainWake: the host keeps an
//     Atomics.waitAsync armed on the word (wasmMainWakeInit) and calls
//     resume when it is notified. notewakeup(&m0.park) nudges, so every
//     existing wake path (startm, startlockedm, startTheWorld) works
//     unchanged; pidleput nudges when a parked main M waits for a P.
//
// A resume while parked enters handleEvent on g0, which returns through
// wasmThreadsHandleEventEntry -> wasmMainParkWake:
//
//   - If m0.park was woken, the park loop exits and stopm proceeds with
//     the nextp the waker installed (the unchanged protocol).
//   - Otherwise (a host timeout/event or a nudge), the main M re-enters
//     the scheduler on its own: it removes itself from the idle M list,
//     grabs an idle P, self-completes the stopm protocol, and lets
//     findRunnable run timers and - via wasmPendingJSEntry in beforeIdle -
//     spawn the event goroutine that handles the pending host event. If
//     every P is busy it sets wasmMainWantsP and parks again; pidleput
//     nudges it as soon as a P frees.
//
// While parked with active worker Ms the host is told to keep its event
// loop alive (wasmSetKeepAlive): the workers are unref'd, so otherwise the
// process could exit underneath running Go code. A fully idle program
// drops the keep-alive so the host's exit-time deadlock probe still fires
// (checkdead defers to it via eventLoopCanWake).

// wasmMainParked is nonzero while the main M is parked in the event loop
// from wasmMainParkNote (set/cleared on the main thread only).
var wasmMainParked uint32

// wasmPendingJSEntry is set when a parked main M was resumed by the host
// and re-entered the scheduler; beforeIdle turns it into an event
// goroutine that runs the syscall/js event handler. Main thread only.
var wasmPendingJSEntry uint32

// wasmMainWakeInited records that wasmMainWakeInit was called (main
// thread only).
var wasmMainWakeInited bool

// wasmKeepAliveState caches the last wasmSetKeepAlive value (main thread
// only; -1 = never set).
var wasmKeepAliveState int32 = -1

// wasmMainParkNote parks the main M on its park note in the host's event
// loop. Called from notesleep (i.e. mPark) on m0's g0.
func wasmMainParkNote(n *note) {
	if !wasmMainWakeInited {
		wasmMainWakeInited = true
		wasmMainWakeInit(&wasmMainWake)
	}
	gp := getg()
	for atomic.Load(key32(&n.key)) == 0 {
		// Keep the host's event loop alive while other Ms are still
		// running Go code (see the comment block above).
		on := int32(0)
		if wasmActiveMs() > 0 {
			on = 1
		}
		if wasmKeepAliveState != on {
			wasmKeepAliveState = on
			wasmSetKeepAlive(on)
		}
		atomic.Store(&wasmMainParked, 1)
		idleStart = nanotime()
		gp.m.blocked = true
		wasmMainPause()
		// A resume came in: handleEvent ran wasmMainParkWake on this
		// stack and returned here (see the pause contract in
		// stubs_wasm.go). Re-check the note and park again if it was
		// not a real wakeup.
		gp.m.blocked = false
	}
	atomic.Store(&wasmMainWantsP, 0)
}

// wasmMainPause returns to the host's event loop. The next resume runs
// handleEvent in place of this frame; when it returns, control continues
// at this call site in wasmMainParkNote.
func wasmMainPause() {
	pause(sys.GetCallerSP() - 16)
}

// wasmThreadsHandleEventEntry intercepts a resume that woke a main M
// parked in the event loop. It runs on m0's g0 with no P. Reports whether
// the resume was consumed (handleEvent then just returns, continuing the
// park loop).
func wasmThreadsHandleEventEntry() bool {
	if atomic.Load(&wasmMainParked) == 0 {
		return false
	}
	atomic.Store(&wasmMainParked, 0)
	sched.idleTime.Add(nanotime() - idleStart)
	wasmMainParkWake()
	return true
}

// wasmMainParkWake decides what a resumed, parked main M does next. See
// the comment block above. Runs on m0's g0 with no P, so no write
// barriers and no allocation.
//
//go:nowritebarrierrec
func wasmMainParkWake() {
	if atomic.Load(key32(&m0.park.key)) != 0 {
		// Real wakeup: the waker installed nextp; the park loop exits
		// and stopm proceeds.
		return
	}
	// Host resume (timeout, event, or nudge): re-enter the scheduler.
	lock(&sched.lock)
	if !wasmMidleRemove(&m0) {
		// Not on the idle M list: either a concurrent startm just
		// claimed us (it will wake the park note), or we are parked in
		// stoplockedm (a locked goroutine owns this M; only its readying
		// can unpark us). Park again; a pending host event is handled
		// once we are unparked. If a timer is due somewhere, re-arm the
		// running goroutines' checks so a busy P yields and fires it -
		// we cannot.
		if next := wasmEarliestTimerWake(); next != 0 && nanotime() >= next {
			wasmThreadsKick()
		}
		unlock(&sched.lock)
		return
	}
	pp, _ := pidleget(0)
	if pp == nil {
		// Every P is busy. Put ourselves back and keep parking;
		// pidleput nudges us the moment a P frees. If a timer is due
		// somewhere, re-arm the running goroutines' preemption checks
		// so one of the busy Ps yields and fires it (with multiple Ps
		// the gate disarms for far-future timers; this is the re-arm).
		atomic.Store(&wasmMainWantsP, 1)
		mput(&m0)
		if next := wasmEarliestTimerWake(); next != 0 && nanotime() >= next {
			wasmThreadsKick()
		}
		unlock(&sched.lock)
		return
	}
	atomic.Store(&wasmMainWantsP, 0)
	m0.nextp.set(pp)
	unlock(&sched.lock)
	wasmPendingJSEntry = 1
	// Self-complete the stopm protocol: the park loop exits and stopm
	// acquires nextp.
	notewakeup(&m0.park)
}

// wasmMidleRemove unlinks mp from sched.midle. sched.lock must be held.
// Reports whether mp was found (false: a concurrent mget claimed it, or
// it was never on the list - e.g. stoplockedm).
//
//go:nowritebarrierrec
func wasmMidleRemove(mp *m) bool {
	assertLockHeld(&sched.lock)
	// Membership test: on the (doubly-linked, intrusive) idle list mp is
	// either the head or has a nonzero neighbor link; off the list both
	// links are zero (pop and remove clear them).
	if sched.midle.head() != unsafe.Pointer(mp) && mp.idleNode.prev == 0 && mp.idleNode.next == 0 {
		return false
	}
	sched.midle.remove(unsafe.Pointer(mp))
	sched.nmidle--
	return true
}

// wasmActiveMs returns the number of Ms currently running Go code (not
// parked idle). The calling (parked) main M is already accounted idle by
// stopm/stoplockedm.
func wasmActiveMs() int32 {
	lock(&sched.lock)
	n := mcount() - sched.nmidle - sched.nmidlelocked - sched.nmsys
	unlock(&sched.lock)
	return n
}

// wasmNewEventG creates the goroutine that handles a pending host event,
// parked, for beforeIdle to hand straight to findRunnable. It never goes
// on a run queue, so it cannot be stolen by a worker M.
func wasmNewEventG() *g {
	fn := wasmEventGoroutine // func value, so the funcval is addressable
	return newproc1(*(**funcval)(unsafe.Pointer(&fn)), getg(), sys.GetCallerPC(), true, waitReasonZero)
}

// wasmEventGoroutine handles one host event on the main M under
// GOWASM=threads: the asynchronous counterpart of handleEvent (which
// keeps serving the synchronous, nested case). It locks itself to the
// main M so that an event handler that blocks and is readied from a
// worker thread still continues on the main thread (JavaScript values
// and the events bookkeeping are main-thread-only); while it is blocked,
// the main M parks in stoplockedm - in the event loop, via
// wasmMainParkNote - and the P moves to a worker.
//
// Unlike handleEvent it does not pause back to the host when done: the
// goroutine simply exits, and the main M returns to the host when it goes
// idle (wasmMainParkNote). Nothing here keeps the world stopped, so other
// Ms keep running throughout.
func wasmEventGoroutine() {
	// This goroutine is created by (and normally starts on) the main M,
	// but it is an ordinary goroutine: a preemption before or during the
	// handler can put it on a run queue where a worker M picks it up.
	// Whenever that happens, migrate back (the same primitive syscall/js
	// uses): the events bookkeeping, the timeout imports, and the event
	// handler's JavaScript access are main-thread-only.
	if getg().m != &m0 {
		if !wasmThreadsMigrateToMain() {
			throw("wasm: event goroutine stuck off the main M")
		}
	}

	e := &event{
		gp:       getg(),
		returned: false,
	}
	events = append(events, e)

	if eventHandler == nil {
		// See handleEvent: without syscall/js the only host event is
		// the exit-time deadlock probe. Park forever so checkdead
		// reports the deadlock.
		deadlockProbeActive = true
		clearIdleTimeout()
		gopark(nil, nil, waitReasonZero, traceBlockGeneric, 1)
		throw("unreachable") // gopark above never returns
	}

	handled := eventHandler()

	if getg().m != &m0 {
		// The handler blocked and we were readied from a worker thread.
		wasmThreadsMigrateToMain()
	}

	if !handled {
		// No window event was handled, so this resume was a timeout
		// (the idle timeout or the forced-GC nudge); clear both, they
		// are re-armed by beforeIdle when still needed.
		clearIdleTimeout()
		clearIdleGCNudge()
	}

	// Pop this event. It is usually the top of the stack, but another
	// event goroutine can have been pushed (and not yet popped) while
	// our handler was blocked, so search. All events bookkeeping runs
	// on the main M (see the migrations above).
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] == e {
			copy(events[i:], events[i+1:])
			events[len(events)-1] = nil
			events = events[:len(events)-1]
			break
		}
	}
}

// wasmEarliestTimerWake returns the earliest timer wakeup time across all
// Ps, or 0 if none. The main M consults it before parking so its
// JavaScript timeout also covers timers on Ps that are (or go) idle.
func wasmEarliestTimerWake() int64 {
	next := int64(0)
	for _, pp := range allp {
		if pp == nil {
			continue
		}
		if w := pp.timers.wakeTime(); w != 0 && (next == 0 || w < next) {
			next = w
		}
	}
	return next
}

// wasmThreadsBeforeIdleMain is beforeIdle on the main M: arm the
// JavaScript timeout machinery (like eventBeforeIdle, but for the
// earliest timer across all Ps) and turn a pending host resume into the
// event goroutine. It never pauses here; an idle main M parks via the
// ordinary stopm path (see wasmMainParkNote).
//
//go:yeswritebarrierrec
func wasmThreadsBeforeIdleMain(now, pollUntil int64) (gp *g, otherReady bool) {
	if wasmPendingJSEntry != 0 {
		// A host resume (timeout or event) brought us back: run the
		// syscall/js event handler. findRunnable executes the returned
		// goroutine directly on this M.
		wasmPendingJSEntry = 0
		return wasmNewEventG(), false
	}

	// The main M's timeout backstops every P's timers (a worker M about
	// to abandon a P with timers cannot arm host timeouts; see
	// wasmThreadsPidleput).
	if t := wasmEarliestTimerWake(); t != 0 && (pollUntil == 0 || t < pollUntil) {
		pollUntil = t
	}
	if pollUntil != 0 && now == 0 {
		now = nanotime()
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
		// No timer will wake the program: arm the weak forced-GC nudge,
		// exactly like eventBeforeIdle (see there for why it must be
		// weak).
		if deadline := wasmForceGCDeadline(); deadline != 0 && (idleGCNudge == nil || idleGCNudge.diff(deadline) > 1e6) {
			idleGCNudge.clear()

			if now == 0 {
				now = nanotime()
			}
			nudgeDelay := (deadline-now-1)/1e6 + 1
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

	return nil, false
}

// beforeIdle gets called by the scheduler if no goroutine is awake.
//
// On the main M: arm the host timeout machinery and hand a pending host
// event to the scheduler (wasmThreadsBeforeIdleMain); with nothing to do
// the main M falls through to stopm and parks in the event loop
// (wasmMainParkNote).
//
// On a worker M with pending timers on its P: sleep on the scheduler
// nudge word until (at most) the earliest deadline, capped so the M
// re-examines the world regularly, and report otherReady so findRunnable
// loops and fires the timers. The nudge word is bumped by preemptall
// (stop-the-world) and wasmThreadsKick (work with no idle P), so the
// sleep never delays those. With no timers, fall through so the M parks
// in stopm on its futex note.
//
//go:yeswritebarrierrec
func beforeIdle(now, pollUntil int64) (gp *g, otherReady bool) {
	if getg().m == &m0 {
		return wasmThreadsBeforeIdleMain(now, pollUntil)
	}
	if pollUntil != 0 {
		if wasmMigrateCount.Load() != 0 || atomic.Load(&wasmMainWantsP) != 0 {
			// The main M needs a P: goroutines are waiting on the
			// migrate queue (only the main M's findRunnable can pop
			// them) or a resumed main M could not get a P for a pending
			// host event (wasmMainWantsP). Holding this P through a
			// timed idle sleep would starve it - with GOMAXPROCS=1 this
			// very loop once held the ONLY P for the entire timer wait
			// while a migrated goroutine sat unrunnable (observed:
			// sync.test's runExamples stuck in runtimeMigrateToMain
			// until the test timeout). Fall through to findRunnable's
			// ordinary give-up path instead: releasep + pidleput, whose
			// GOWASM=threads hook nudges the main M, which then
			// self-serves the P. This P's timers stay covered while it
			// is idle: the main M's JS timeout spans all Ps, and parked
			// workers' timed parks use the global earliest deadline.
			return nil, false
		}
		if now == 0 {
			now = nanotime()
		}
		ns := pollUntil - now
		if ns > 10e6 {
			ns = 10e6 // cap: re-check the scheduler state regularly
		}
		if ns > 0 {
			v := atomic.Load(&wasmSchedNudge)
			if !sched.gcwaiting.Load() {
				t0 := nanotime()
				futexsleep(&wasmSchedNudge, v, ns)
				// The slept time is CPU idle time (this M held an
				// otherwise-empty P waiting for its timers); account it
				// like the event-loop pauses do, so /cpu/classes/idle
				// metrics stay meaningful.
				sched.idleTime.Add(nanotime() - t0)
			}
		}
		return nil, true
	}
	return nil, false
}
