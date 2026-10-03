// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import "unsafe"

// Pipe-backed, async-signal-safe implementation of the one note used by
// sigqueue, for XNU hosts. This is a port of the darwin sigNote
// machinery in os_darwin.go: on macOS, M parking is pthread-based
// (semawakeup takes a pthread mutex), which is not async-signal-safe,
// so sigsend - which runs in the signal handler - cannot use notewakeup
// there. It writes a byte into this pipe instead, and signal_recv
// blocks in read(2) rather than in notesleep. The choice is made at run
// time through sigNoteUsed (sigqueue.go), set by osArchInit only when
// the host is XNU; on Linux hosts sigqueue keeps using regular notes,
// whose futex-backed semawakeup is async-signal-safe.

// The read and write file descriptors used by the sigNote functions.
var sigNoteRead, sigNoteWrite int32

// sigNoteSetup initializes a single, there-can-only-be-one,
// async-signal-safe note.
func sigNoteSetup(*note) {
	if sigNoteRead != 0 || sigNoteWrite != 0 {
		// Generalizing this would require avoiding the pipe-fork-closeonexec race, which entangles syscall.
		throw("duplicate sigNoteSetup")
	}
	r, w, errno := pipe2(_O_CLOEXEC)
	if errno != 0 {
		throw("pipe failed")
	}
	sigNoteRead = r
	sigNoteWrite = w

	// Make the write end of the pipe non-blocking, so that if the pipe
	// buffer is somehow full we will not block in the signal handler.
	// Leave the read end of the pipe blocking so that we will block
	// in sigNoteSleep.
	const (
		_F_GETFL = 3
		_F_SETFL = 4
	)
	fl, e := fcntl(w, _F_GETFL, 0)
	if e != 0 {
		throw("sigNoteSetup: fcntl F_GETFL")
	}
	if _, e := fcntl(w, _F_SETFL, fl|_O_NONBLOCK); e != 0 {
		throw("sigNoteSetup: fcntl F_SETFL")
	}
}

// sigNoteWakeup wakes up a thread sleeping on a note created by sigNoteSetup.
func sigNoteWakeup(*note) {
	var b byte
	for {
		n := write(uintptr(sigNoteWrite), unsafe.Pointer(&b), 1)
		if n != -_EINTR {
			// Success, -EAGAIN (pipe full: a wakeup byte is already
			// pending for the receiver) and everything else mean the
			// wakeup is delivered or unrecoverable. Only an
			// interrupted write must be retried - SIGURG preemption
			// interrupts libc calls on XNU, and an unretried EINTR
			// here would lose the wakeup (the same hardening as the
			// netpoller's wakeup-pipe writers).
			return
		}
	}
}

// sigNoteSleep waits for a note created by sigNoteSetup to be woken.
func sigNoteSleep(*note) {
	for {
		var b byte
		entersyscallblock()
		n := read(sigNoteRead, unsafe.Pointer(&b), 1)
		exitsyscall()
		if n != -_EINTR {
			return
		}
	}
}
