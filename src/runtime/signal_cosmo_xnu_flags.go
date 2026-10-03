// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// Apple sigaction flag values (upstream defs_darwin_*.go). The Linux
// values these translate from are in defs_cosmo_*.go: _SA_SIGINFO 0x4,
// _SA_ONSTACK 0x8000000, _SA_RESTART 0x10000000.
//
// This file carries no architecture tag on purpose. arm64 reaches Apple
// sigaction through the APE loader's Syslib and amd64 through the raw
// __sigaction syscall, but the FLAGS are the same on both, and a second
// copy would drift the first time somebody corrected an entry.
const (
	xnuSA_ONSTACK = 0x1
	xnuSA_RESTART = 0x2
	xnuSA_SIGINFO = 0x40
)

// xnuSigFlagsL2A translates Linux sigaction flags to Apple's.
//
// Only these three cross over. Anything else Linux defines either has no
// Apple counterpart or is meaningless to the runtime, and passing an
// unknown bit through would set whatever Apple happens to use that bit
// for.
//
//go:nosplit
//go:nowritebarrierrec
func xnuSigFlagsL2A(fl uint64) int32 {
	var a int32
	if fl&_SA_SIGINFO != 0 {
		a |= xnuSA_SIGINFO
	}
	if fl&_SA_ONSTACK != 0 {
		a |= xnuSA_ONSTACK
	}
	if fl&_SA_RESTART != 0 {
		a |= xnuSA_RESTART
	}
	return a
}

// xnuSigFlagsA2L translates Apple sigaction flags back to Linux's.
//
//go:nosplit
//go:nowritebarrierrec
func xnuSigFlagsA2L(a int32) uint64 {
	var fl uint64
	if a&xnuSA_SIGINFO != 0 {
		fl |= _SA_SIGINFO
	}
	if a&xnuSA_ONSTACK != 0 {
		fl |= _SA_ONSTACK
	}
	if a&xnuSA_RESTART != 0 {
		fl |= _SA_RESTART
	}
	return fl
}
