// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

// extraMSema is a counting semaphore for the threads that sleep in
// lockextra. Those threads may have no m or g, so they sleep on this one
// libc semaphore rather than an m's, through sem_wait1 and sem_post1, which
// call libc without the m that syscall1 needs. osinit initializes it.
var extraMSema semt

func extraMSemaInit() {
	if sem_init(&extraMSema, 0, 0) != 0 {
		throw("sem_init")
	}
}

// sem_wait1 and sem_post1 are in sys_aix_ppc64.s.
func sem_wait1(sem *semt) int32
func sem_post1(sem *semt) int32

// extraMSemaSleep sleeps until it takes one wakeup. A sem_wait that a
// signal interrupts takes none and is made again.
//
//go:nosplit
func extraMSemaSleep() {
	for sem_wait1(&extraMSema) != 0 {
	}
}

// extraMSemaWake posts count wakeups.
//
//go:nosplit
func extraMSemaWake(count uint32) {
	for range count {
		sem_post1(&extraMSema)
	}
}
