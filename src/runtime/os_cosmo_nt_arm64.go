// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import "unsafe"

// Windows NT is amd64-only (Windows/arm64 is out of scope for the NT
// bring-up). iswindows is constant false here so the shared cosmo
// code's NT branches compile away, and the nt* stubs below exist only
// so that shared code links - they are unreachable. Mirrors the
// darwinSigprocmask/darwinSigaction stub idiom on amd64.

//go:nosplit
func iswindows() bool {
	return false
}

//go:nosplit
func ntFutexsleep(addr *uint32, val uint32, ns int64) {
	throw("ntFutexsleep: not implemented on arm64")
}

//go:nosplit
func ntFutexwakeup(addr *uint32) {
	throw("ntFutexwakeup: not implemented on arm64")
}

func ntNewosproc(mp *m) {
	throw("ntNewosproc: not implemented on arm64")
}

func ntNumCPU() int32 {
	return 1
}

//go:nosplit
func ntVirtualAlloc(v unsafe.Pointer, n uintptr, allocType, prot uintptr) unsafe.Pointer {
	throw("ntVirtualAlloc: not implemented on arm64")
	return nil
}

func cosmoNTGoargs() bool {
	return false
}

func ntReadRandom(r []byte) int {
	return 0
}

func ntGoenvs() {
	throw("ntGoenvs: not implemented on arm64")
}

//go:nosplit
func ntVirtualFree(v unsafe.Pointer, n uintptr, freeType uintptr) uintptr {
	throw("ntVirtualFree: not implemented on arm64")
	return 0
}

// NT netpoller stubs (netpoll_cosmo.go dispatches here only when
// iswindows(), which is constant false on arm64).

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
