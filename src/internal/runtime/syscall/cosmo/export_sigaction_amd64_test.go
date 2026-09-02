// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package cosmo

import "unsafe"

// Exports for sigaction_cosmo_amd64_test.go.

var SigA2LTab = &sigA2LTab

// XnuKsigactionLayout reports the size and field offsets of Apple's
// kernel struct sigaction, so a test can pin them.
func XnuKsigactionLayout() (size, handler, tramp, mask, flags uintptr) {
	var s xnuKsigactiont
	return unsafe.Sizeof(s), unsafe.Offsetof(s.handler), unsafe.Offsetof(s.tramp),
		unsafe.Offsetof(s.mask), unsafe.Offsetof(s.flags)
}
