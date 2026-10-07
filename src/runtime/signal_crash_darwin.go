// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build darwin

package runtime

// The crash thread sleeps on crashEvent, which signal handlers can wake.
// These run only if crashWaitInit could not create its kqueue. A darwin
// note cannot be woken from a signal handler (semawakeup throws there),
// so without the kqueue the crash thread rechecks crashing between short
// timed sleeps.

func crashSleepInit() {}

// crashSleep sleeps for ns nanoseconds, or 5ms if that is shorter.
func crashSleep(gen uint32, ns int64) {
	const sliceMicros = 5000
	usec := uint32(sliceMicros)
	if ns < sliceMicros*1000 {
		usec = uint32(ns/1000) + 1
	}
	usleep(usec)
}

func crashWakeSleeper() {}
