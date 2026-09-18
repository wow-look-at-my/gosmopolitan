// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import "unsafe"

// Exports for sigxlat_cosmo_test.go.

var CosmoSigL2A = cosmoSigL2A
var CosmoSigA2L = cosmoSigA2L
var CosmoSigmaskL2A = cosmoSigmaskL2A
var CosmoSigmaskA2L = cosmoSigmaskA2L

// Exports for sigprof_cosmo_test.go: the Apple itimerval ABI pins and
// the timeval translation (signal_cosmo_itimer.go), surfaced over
// plain integers since the struct types are unexported.

const (
	XnuTimevalSize       = unsafe.Sizeof(xnuTimeval{})
	XnuItimervalSize     = unsafe.Sizeof(xnuItimerval{})
	XnuItimervalUsec1Off = unsafe.Offsetof(xnuItimerval{}.it_interval) + unsafe.Offsetof(xnuTimeval{}.tv_usec)
	XnuItimervalUsec2Off = unsafe.Offsetof(xnuItimerval{}.it_value) + unsafe.Offsetof(xnuTimeval{}.tv_usec)
)

func CosmoTimevalL2X(sec, usec int64) (int64, int32) {
	x := cosmoTimevalL2X(&timeval{tv_sec: sec, tv_usec: usec})
	return x.tv_sec, x.tv_usec
}

func CosmoTimevalX2L(sec int64, usec int32) (int64, int64) {
	l := cosmoTimevalX2L(&xnuTimeval{tv_sec: sec, tv_usec: usec})
	return l.tv_sec, l.tv_usec
}

// Export for futex_cosmo_test.go: one step of the darwin futex wait.

func DarwinFutexDelay(sleep uint32, leftNsec int64, timed bool) (uint32, bool) {
	return darwinFutexDelay(sleep, leftNsec, timed)
}
