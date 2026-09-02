// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

// iswindows reports whether the host is Windows NT.
//
// It is constant false on arm64. The APE carries no Windows/arm64 boot
// stub, so nothing ever sets __hostos to _HOSTWINDOWS on this
// architecture, and the shared cosmo code's NT branches compile away.
// The runtime surface underneath them is real - os_cosmo_nt*.go and
// sys_cosmo_nt_arm64.s build for arm64 - so what is missing is the
// boot path that would reach it, plus the syscall-emulation layer
// described below.
//
//go:nosplit
func iswindows() bool {
	return false
}

// The syscall-emulation layer (os_cosmo_nt_sys.go, and the fd, socket,
// message, path, metadata, exec, DNS and certificate files under it)
// stays amd64 only. It is written against LINUX AMD64 syscall
// numbering, struct stat and O_* flag values, and all three differ on
// arm64. The stubs below stand in for the parts of that layer the
// shared cosmo code names by hand. Nothing can reach them, because
// iswindows() is false.

// ntReadRandom reports that it filled no bytes, which is what
// readRandom (os_cosmo.go) reads as "ask the next source".
func ntReadRandom(r []byte) int {
	return 0
}

// NT netpoller stubs (netpoll_cosmo.go dispatches here only when
// iswindows(), which is constant false on arm64). The netpoller sits
// on WSAPoll and the fd table, both inside the emulation layer.

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
