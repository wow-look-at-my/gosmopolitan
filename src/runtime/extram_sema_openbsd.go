// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import "unsafe"

// extraMWordSleep sleeps while *addr is 0. It may return early. It needs
// no m or g. __thrsleep reads its abort word, addr itself, after it queues
// the thread and returns at once if the word is not 0.
//
//go:nosplit
func extraMWordSleep(addr *uint32) {
	thrsleep(uintptr(unsafe.Pointer(addr)), _CLOCK_MONOTONIC, nil, 0, addr)
}

// extraMWordWake wakes one thread sleeping on addr.
//
//go:nosplit
func extraMWordWake(addr *uint32) {
	thrwakeup(uintptr(unsafe.Pointer(addr)), 1)
}
