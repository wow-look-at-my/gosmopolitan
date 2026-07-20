// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && !arm64

package runtime

import "internal/runtime/atomic"

// mOS contains OS-specific m fields for cosmo on non-arm64 architectures.
// This version uses uint32 for waitsema for futex-based locking.
type mOS struct {
	// profileTimer holds the ID of the POSIX interval timer for profiling CPU
	// usage on this thread.
	profileTimer      int32
	profileTimerValid atomic.Bool

	// needPerThreadSyscall indicates that a per-thread syscall is required
	// for doAllThreadsSyscall.
	needPerThreadSyscall atomic.Uint8

	// waitsema is used as a futex for lock_futex.go
	waitsema uint32

	// NT (iswindows) fields, inert on Linux hosts. Mirrors upstream
	// os_windows.go's mOS preemption trio.

	// thread is a duplicated handle of this thread for use by
	// ntPreemptM (SuspendThread-based async preemption); 0 when the M
	// is not minit'd. Accesses are protected by threadLock: closing
	// the handle out from under an in-flight DuplicateHandle in
	// ntPreemptM would hand the suspend machinery a dead handle.
	thread uintptr

	// threadLock protects thread and its duplication window.
	threadLock mutex

	// preemptExtLock synchronizes ntPreemptM with entry into and exit
	// from external (win64) code on this thread. This protects
	// against races between ntPreemptM calling SuspendThread and
	// external code on this thread calling ExitProcess. If these
	// happen concurrently, it's possible for ExitProcess to acquire
	// the loader lock and then get suspended: the process deadlocks.
	//
	// 0 means this M is not being preempted or in external code.
	// Entering external code CASes this from 0 to 1. If this fails,
	// a preemption is in progress, so the thread must wait for the
	// preemption. ntPreemptM also CASes this from 0 to 1. If this
	// fails, the preemption fails (as it would if the PC weren't in
	// Go code). Upstream os_windows.go's field, verbatim semantics.
	preemptExtLock uint32

	// ntLastError is this thread's GetLastError value, captured by
	// the ntcall6/ntcall10 trampolines (sys_cosmo_nt_amd64.s)
	// immediately after the foreign call returns - atomically with
	// the call, before any window in which the thread could be
	// suspended or another win64 call could clobber the TEB slot.
	// Read via getg().m by ntcallE and friends.
	ntLastError uint32
}
