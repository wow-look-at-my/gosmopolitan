// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// Apple itimerval mirror types and the Linux<->Apple translation the
// darwin setitimer dispatch needs (darwinSetitimer,
// signal_cosmo_xnu.go). Deliberately arch-independent (cosmo's
// timeval is {int64, int64} on both GOARCHes) and free of any libc
// plumbing, so sigprof_cosmo_test.go can pin the layout and the
// translation under GOOS=cosmo on any host - the
// socket_msg_cosmo.go precedent.

// xnuTimeval is Apple's struct timeval (upstream defs_darwin_arm64.go):
// tv_usec is a 32-bit suseconds_t followed by explicit padding, unlike
// Linux's int64. Both itimervals are 32 bytes, but the usec words sit
// at offsets 8/24 here versus full int64s on Linux - raw structs must
// never cross the boundary.
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

// cosmoTimevalL2X translates a Linux timeval to Apple's layout. The
// int64->int32 usec narrowing is lossless for every value the runtime
// produces: setProcessCPUProfilerTimer stores at most 1e6-1 (via
// set_usec's int32 parameter).
func cosmoTimevalL2X(l *timeval) xnuTimeval {
	return xnuTimeval{tv_sec: l.tv_sec, tv_usec: int32(l.tv_usec)}
}

// cosmoTimevalX2L translates an Apple timeval to the Linux layout.
func cosmoTimevalX2L(x *xnuTimeval) timeval {
	return timeval{tv_sec: x.tv_sec, tv_usec: int64(x.tv_usec)}
}
