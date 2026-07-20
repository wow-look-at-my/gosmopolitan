// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime

// sleep and wakeup on one-time events.
// before any calls to notesleep or notewakeup,
// must call noteclear to initialize the Note.
// then, exactly one thread can call notesleep
// and exactly one thread can call notewakeup (once).
// once notewakeup has been called, the notesleep
// will return.  future notesleep will return immediately.
// subsequent noteclear must be called only after
// previous notesleep has returned, e.g. it's disallowed
// to call noteclear straight after notewakeup.
//
// notetsleep is like notesleep but wakes up after
// a given number of nanoseconds even if the event
// has not yet happened.  if a goroutine uses notetsleep to
// wake up early, it must wait to call noteclear until it
// can be sure that no other goroutine is calling
// notewakeup.
//
// notesleep/notetsleep are generally called on g0,
// notetsleepg is similar to notetsleep but is called on user g.
type note struct {
	// key is treated as a uint32 futex word: 0 while the note is
	// pending, 1 once it has been woken. Ms (g0) block on it with
	// memory.atomic.wait32; see lock_jsthreads.go.
	key uintptr

	// gp is a goroutine parked on the note by notetsleepg(n, -1),
	// which parks the g instead of blocking the M so that the
	// event-loop M stays responsive. Guarded by noteGLock.
	//
	// It is a guintptr, not a *g, so that noteclear/notewakeup stay
	// free of write barriers (they run in nowritebarrierrec contexts
	// like mPark and templateThread); the g is kept alive by allgs.
	gp guintptr
}
