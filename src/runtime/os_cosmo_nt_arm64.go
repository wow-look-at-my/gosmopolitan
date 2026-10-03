// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import _ "unsafe" // for go:linkname

//go:linkname poll_runtime_cancelIO internal/poll.runtime_cancelIO
func poll_runtime_cancelIO(fd uintptr) {}

// iswindows reports whether the host is Windows NT.
//
//go:nosplit
func iswindows() bool {
	return false
}

// The syscall-emulation layer.

// ntReadRandom reports that it filled no bytes, which is what
// readRandom (os_cosmo.go) reads as "ask the next source".
func ntReadRandom(r []byte) int {
	return 0
}

// NT netpoller stubs (netpoll_cosmo.go dispatches here only when iswindows(),
// which is constant false on arm64).

func netpollinitNT() {
	throw("netpollinitNT: not implemented on arm64")
}

func netpollopenNT(fd uintptr, pd *pollDesc) uintptr {
	throw("netpollopenNT: not implemented on arm64")
	return 38 // ENOSYS
}

func netpollcloseNT(fd uintptr) uintptr {
	throw("netpollcloseNT: not implemented on arm64")
	return 38 // ENOSYS
}

func netpollarmNT(pd *pollDesc, mode int) {
	throw("netpollarmNT: not implemented on arm64")
}

func netpollBreakNT() {
	throw("netpollBreakNT: not implemented on arm64")
}

func netpollNT(delay int64) (gList, int32) {
	throw("netpollNT: not implemented on arm64")
	return gList{}, 0
}
