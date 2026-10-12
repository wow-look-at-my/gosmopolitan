// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || darwin

package runtime

// Operation codes and flags of __ulock_wait and __ulock_wake, the XNU
// futex operations, as bsd/sys/ulock.h names them.
const (
	_UL_COMPARE_AND_WAIT = 1
	_ULF_WAKE_ALL        = 0x00000100
	_ULF_NO_ERRNO        = 0x01000000
)

// xnuUlockTimeout converts nanoseconds into __ulock_wait's microseconds,
// where no timeout at all waits forever. It rounds up, so that the wait
// does not end early.
//
//go:nosplit
func xnuUlockTimeout(ns int64) uint32 {
	if ns < 0 {
		return 0
	}
	usec := (ns + 999) / 1000
	if usec == 0 {
		return 1
	}
	if usec > 1<<32-1 {
		return 1<<32 - 1
	}
	return uint32(usec)
}
