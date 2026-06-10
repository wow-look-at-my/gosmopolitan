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
}
