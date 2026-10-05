// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import "internal/runtime/atomic"

// mOS contains OS-specific m fields for cosmo arm64. The host OS is
// only known at run time. It carries both hosts' M-parking state: a
// pthread mutex/cond pair for XNU and a futex word for Linux (see
// os_cosmo_arm64_sema.go).
type mOS struct {
	// profileTimer holds the ID of the POSIX interval timer for profiling CPU usage on this thread.
	profileTimer      int32
	profileTimerValid atomic.Bool

	// needPerThreadSyscall indicates that a per-thread syscall is required for doAllThreadsSyscall.
	needPerThreadSyscall atomic.Uint8

	// M parking on XNU hosts, mirroring upstream os_darwin.go's mOS field for field: count is the semaphore value, guarded by mutex.
	initialized bool
	mutex       pthreadmutex
	cond        pthreadcond
	count       int

	// waitsemacount is the futex word backing the semaphore on Linux hosts, where the pthread pair above is never touched.
	waitsemacount uint32

	// preemptExtLock synchronizes ntPreemptM with entry into and exit from external (win64) code on this thread.
	preemptExtLock uint32

	thread uintptr

	// threadLock protects thread and its duplication window.
	threadLock mutex

	// ntLastError is this thread's GetLastError value.
	ntLastError uint32
}

// pthreadmutex reserves the size and alignment of Apple's pthread_mutex_t.
type pthreadmutex struct {
	sig    int64
	opaque [56]int8
}

type pthreadcond struct {
	sig    int64
	opaque [40]int8
}
