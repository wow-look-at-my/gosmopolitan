// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

// extraMSemaCount is a counting semaphore for the threads that sleep in
// lockextra, held by the kernel's semacquire and semrelease, which need no
// m or g.
var extraMSemaCount uint32

// extraMSemaSleep sleeps until it takes one wakeup. A semacquire that a
// note interrupts takes none and is made again.
//
//go:nosplit
func extraMSemaSleep() {
	for plan9_semacquire(&extraMSemaCount, 1) < 0 {
	}
}

// extraMSemaWake posts count wakeups.
//
//go:nosplit
func extraMSemaWake(count uint32) {
	plan9_semrelease(&extraMSemaCount, int32(count))
}
