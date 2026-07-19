// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Fake network poller for js/wasm.
//
// There is nothing to poll on js: network connections are serviced by
// the host and do not honor "SetNonblock", so the poller hooks below
// are all no-ops. netpoll itself is nonetheless called regularly: as
// soon as timers exist they force netpollGenericInit, and findRunnable
// then calls netpoll on every idle cycle. It returns immediately,
// ignoring the requested delay. The actual sleeping happens in
// beforeIdle (lock_js.go), which schedules a JavaScript timeout event
// and yields to the host's event loop. When the runtime cannot yield -
// e.g. while a synchronous call from JavaScript is still on the stack -
// the scheduler instead spins through netpoll until the next timer is
// due.

//go:build js && wasm

package runtime

func netpollinit() {
}

func netpollIsPollDescriptor(fd uintptr) bool {
	return false
}

func netpollopen(fd uintptr, pd *pollDesc) int32 {
	return 0
}

func netpollclose(fd uintptr) int32 {
	return 0
}

func netpollarm(pd *pollDesc, mode int) {
}

func netpollBreak() {
}

func netpoll(delay int64) (gList, int32) {
	return gList{}, 0
}
