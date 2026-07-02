// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import _ "unsafe" // for go:linkname

// Host OS constants (passed in CL by APE loader on x86_64)
const (
	_HOSTLINUX   = 0
	_HOSTMETAL   = 1
	_HOSTWINDOWS = 2
	_HOSTXNU     = 8
	_HOSTFREEBSD = 9
	_HOSTOPENBSD = 10
	_HOSTNETBSD  = 11
)

// __hostos indicates the host operating system.
// Set by rt0_cosmo_amd64.s at startup.
// 0=Linux, 8=XNU/macOS, 9=FreeBSD, etc.
//
//go:linkname __hostos
var __hostos int32

// isdarwin returns true if running on macOS
//
//go:nosplit
func isdarwin() bool {
	return __hostos == _HOSTXNU
}

// osArchInit is a no-op on amd64: the darwin path uses raw XNU syscall
// numbers rather than an APE-loader Syslib, so there is nothing to
// resolve at startup.
func osArchInit() {}

// cosmoDarwinNumCPU: no Syslib on amd64, so no sysctl access; report
// "unknown" and let getCPUCount fall back to 1.
func cosmoDarwinNumCPU() int32 { return 0 }

// pipe2 is implemented in sys_cosmo_amd64.s.
func pipe2(flags int32) (r, w int32, errno int32)
