// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

// extraMSema is a counting semaphore for the threads that sleep in
// lockextra. Those threads may have no m or g, so they sleep on this one
// libc semaphore rather than an m's. osinit initializes it.
var extraMSema semt

func extraMSemaInit() {
	if sem_init(&extraMSema, 0, 0) != 0 {
		throw("sem_init")
	}
}

// extraMSemaSleep sleeps until it takes one wakeup. A sem_wait that a
// signal interrupts takes none and is made again.
//
//go:nosplit
func extraMSemaSleep() {
	for sem_wait(&extraMSema) != 0 {
	}
}

// extraMSemaWake posts count wakeups.
//
//go:nosplit
func extraMSemaWake(count uint32) {
	for range count {
		sem_post(&extraMSema)
	}
}
