// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || linux

package runtime

// extraMWordSleep sleeps while *addr is 0. It may return early. It needs
// no m or g.
//
//go:nosplit
func extraMWordSleep(addr *uint32) {
	futexsleep(addr, 0, -1)
}

// extraMWordWake wakes one thread sleeping on addr.
//
//go:nosplit
func extraMWordWake(addr *uint32) {
	futexwakeup(addr, 1)
}
