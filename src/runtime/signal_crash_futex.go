// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || dragonfly || freebsd || linux

package runtime

// The crash thread sleeps on the futex at crashGen. A futex wait and wake
// are single system calls, so the Ms that wake it can do so from their
// signal handlers.

func crashSleepInit() {}

// crashSleep blocks the crash thread while crashGen holds gen, for at
// most ns nanoseconds.
func crashSleep(gen uint32, ns int64) {
	futexsleep(&crashGen, gen, ns)
}

// crashWakeSleeper wakes the crash thread sleeping in crashSleep.
func crashWakeSleeper() {
	futexwakeup(&crashGen, 1)
}
