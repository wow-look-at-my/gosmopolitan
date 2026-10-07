// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || darwin || dragonfly || freebsd || linux || openbsd || windows

package runtime

import "internal/runtime/atomic"

// extraMSemaCount is a counting semaphore for the threads that sleep in
// lockextra: the number of wakeups extraMSemaWake has posted that no
// sleeper has taken yet. Those threads may have no m or g, so they sleep in
// an OS wait on this word that needs neither, declared per OS as
// extraMWordSleep and extraMWordWake.
var extraMSemaCount uint32

// extraMSemaSleep sleeps until it takes one wakeup.
//
//go:nosplit
func extraMSemaSleep() {
	for {
		count := atomic.Load(&extraMSemaCount)
		if count == 0 {
			extraMWordSleep(&extraMSemaCount)
			continue
		}
		if atomic.Cas(&extraMSemaCount, count, count-1) {
			return
		}
	}
}

// extraMSemaWake posts count wakeups.
//
//go:nosplit
func extraMSemaWake(count uint32) {
	atomic.Xadd(&extraMSemaCount, int32(count))
	for range count {
		extraMWordWake(&extraMSemaCount)
	}
}
