// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// Apple itimerval mirror types and the Linux<->Apple translation the darwin
// setitimer dispatch needs.

// xnuTimeval is Apple's struct timeval (upstream defs_darwin_arm64.go):
// tv_usec is a 32-bit suseconds_t followed by explicit padding.
type xnuTimeval struct {
	tv_sec  int64
	tv_usec int32
	_       [4]byte
}

// xnuItimerval is Apple's struct itimerval.
type xnuItimerval struct {
	it_interval xnuTimeval
	it_value    xnuTimeval
}

// cosmoTimevalL2X translates a Linux timeval to Apple's layout.
func cosmoTimevalL2X(l *timeval) xnuTimeval {
	return xnuTimeval{tv_sec: l.tv_sec, tv_usec: int32(l.tv_usec)}
}

// cosmoTimevalX2L translates an Apple timeval to the Linux layout.
func cosmoTimevalX2L(x *xnuTimeval) timeval {
	return timeval{tv_sec: x.tv_sec, tv_usec: int64(x.tv_usec)}
}
