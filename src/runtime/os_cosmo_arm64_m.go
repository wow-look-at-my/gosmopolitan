// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import "internal/runtime/atomic"

// mOS contains OS-specific m fields for cosmo arm64.
// This version uses uintptr for waitsema to store dispatch_semaphore pointers.
type mOS struct {
	// profileTimer holds the ID of the POSIX interval timer for profiling CPU
	// usage on this thread.
	profileTimer      int32
	profileTimerValid atomic.Bool

	// needPerThreadSyscall indicates that a per-thread syscall is required
	// for doAllThreadsSyscall.
	needPerThreadSyscall atomic.Uint8

	// waitsema stores a dispatch_semaphore_t for lock_sema.go
	// (macOS hosts only).
	waitsema uintptr

	// waitsemacount is the futex word backing the semaphore on Linux
	// hosts, where dispatch_semaphore does not exist.
	waitsemacount uint32
}
