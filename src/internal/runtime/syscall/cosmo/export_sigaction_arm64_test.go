// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// Exports for sigaction_cosmo_arm64_test.go.

// XnuSigactionLayout reports the size and field offsets of Apple's libc
// struct sigaction, so a test can pin them.
func XnuSigactionLayout() (size, handler, mask, flags uintptr) {
	var s xnuSigactiont
	return unsafe.Sizeof(s), unsafe.Offsetof(s.handler),
		unsafe.Offsetof(s.mask), unsafe.Offsetof(s.flags)
}
