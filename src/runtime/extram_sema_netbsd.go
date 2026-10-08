// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// extraMSemaCount is a counting semaphore for the threads that sleep in
// lockextra: the number of wakeups extraMSemaWake has posted that no
// sleeper has taken yet. Those threads may have no m or g. NetBSD has no
// wait on an address, so each sleeper parks its LWP, and a wake unparks
// every LWP on extraMParked to retry the count.
var extraMSemaCount uint32

// extraMParked is a stack of extraMParker records, linked through next.
// The records live on their sleepers' stacks, so it holds no pointers.
var extraMParked uintptr

// An extraMParker is one sleeper in extraMSemaSleep.
type extraMParker struct {
	next  uintptr
	lwp   int32
	woken uint32
}

// extraMSemaSleep sleeps until it takes one wakeup.
//
//go:nosplit
func extraMSemaSleep() {
	for {
		count := atomic.Load(&extraMSemaCount)
		if count != 0 {
			if atomic.Cas(&extraMSemaCount, count, count-1) {
				return
			}
			continue
		}
		var parker extraMParker
		parker.lwp = lwp_self()
		record := uintptr(unsafe.Pointer(&parker))
		for {
			parker.next = atomic.Loaduintptr(&extraMParked)
			if atomic.Casuintptr(&extraMParked, parker.next, record) {
				break
			}
		}
		// A wake that added to the count before this record was on
		// the stack did not see it; unpark everyone, this LWP too.
		if atomic.Load(&extraMSemaCount) != 0 {
			extraMUnparkAll()
		}
		for atomic.Load(&parker.woken) == 0 {
			// An unpark that comes before the park makes the park
			// return at once.
			lwp_park(_CLOCK_MONOTONIC, 0, nil, 0, nil, nil)
		}
	}
}

// extraMSemaWake posts count wakeups.
//
//go:nosplit
func extraMSemaWake(count uint32) {
	atomic.Xadd(&extraMSemaCount, int32(count))
	extraMUnparkAll()
}

// extraMUnparkAll takes every record off extraMParked and unparks its LWP.
// It reads a record before it marks it woken, because its sleeper may
// return as soon as it is.
//
//go:nosplit
func extraMUnparkAll() {
	record := atomic.Xchguintptr(&extraMParked, 0)
	for record != 0 {
		parker := (*extraMParker)(unsafe.Pointer(record))
		record = parker.next
		lwp := parker.lwp
		atomic.Store(&parker.woken, 1)
		lwp_unpark(lwp, nil)
	}
}
