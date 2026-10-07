// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build aix || netbsd || openbsd || solaris

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// The crash thread sleeps on its M's semaphore. On these ports the
// semaphore is a kernel object (_lwp_park, __thrsleep, a POSIX
// semaphore), so the Ms that wake it can do so from their signal
// handlers. A wake that comes before the sleep is counted, so it is not
// lost.

// crashSleeper is the crash thread's M, published before SIGQUIT is
// relayed.
var crashSleeper uintptr

func crashSleepInit() {
	atomic.Storeuintptr(&crashSleeper, uintptr(unsafe.Pointer(getg().m)))
}

// crashSleep blocks the crash thread for at most ns nanoseconds, or until
// crashWakeSleeper.
func crashSleep(gen uint32, ns int64) {
	semasleep(ns)
}

// crashWakeSleeper wakes the crash thread sleeping in crashSleep.
func crashWakeSleeper() {
	if mp := (*m)(unsafe.Pointer(atomic.Loaduintptr(&crashSleeper))); mp != nil {
		semawakeup(mp)
	}
}
