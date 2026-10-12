// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

const (
	_SS_DISABLE  = 2
	_NSIG        = 65
	_SIG_BLOCK   = 0
	_SIG_UNBLOCK = 1
	_SIG_SETMASK = 2
)

// It is hard to tease out exactly how big a Sigset is.
type sigset [2]uint32

var sigset_all = sigset{^uint32(0), ^uint32(0)}

//go:nosplit
//go:nowritebarrierrec
func sigaddset(mask *sigset, i int) {
	(*mask)[(i-1)/32] |= 1 << ((uint32(i) - 1) & 31)
}

func sigdelset(mask *sigset, i int) {
	(*mask)[(i-1)/32] &^= 1 << ((uint32(i) - 1) & 31)
}

//go:nosplit
func sigfillset(mask *uint64) {
	*mask = ^uint64(0)
}
