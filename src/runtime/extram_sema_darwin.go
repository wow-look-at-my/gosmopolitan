// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import "unsafe"

// extraMWordSleep sleeps while *addr is 0. It may return early. It needs
// no m or g.
//
//go:nosplit
func extraMWordSleep(addr *uint32) {
	ulock_wait(_UL_COMPARE_AND_WAIT|_ULF_NO_ERRNO, unsafe.Pointer(addr), 0, 0)
}

// extraMWordWake wakes one thread sleeping on addr.
//
//go:nosplit
func extraMWordWake(addr *uint32) {
	ulock_wake(_UL_COMPARE_AND_WAIT|_ULF_NO_ERRNO, unsafe.Pointer(addr), 0)
}
