// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

// extraMWordSleep sleeps while *addr is 0. It may return early. It needs
// no m or g, so it makes the umtx call itself rather than through
// futexsleep, which switches to the system stack.
//
//go:nosplit
func extraMWordSleep(addr *uint32) {
	sys_umtx_sleep(addr, 0, 0)
}

// extraMWordWake wakes one thread sleeping on addr.
//
//go:nosplit
func extraMWordWake(addr *uint32) {
	futexwakeup(addr, 1)
}
