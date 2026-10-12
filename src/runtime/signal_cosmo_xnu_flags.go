// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// Apple sigaction flag values (upstream defs_darwin_*.go).
const (
	xnuSA_ONSTACK = 0x1
	xnuSA_RESTART = 0x2
	xnuSA_SIGINFO = 0x40
)

// xnuSigFlagsL2A translates Linux sigaction flags to Apple's.
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
