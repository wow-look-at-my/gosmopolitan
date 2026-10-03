// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

import "unsafe"

// Exports for sigaction_cosmo_test.go.

var SigFlagsL2A = sigFlagsL2A
var SigFlagsA2L = sigFlagsA2L

var SigmaskL2A = sigmaskL2A
var SigmaskA2L = sigmaskA2L

// LinuxSigactionLayout reports the size and field offsets of the struct
// rt_sigaction takes, so a test can pin them.
func LinuxSigactionLayout() (size, handler, flags, restorer, mask uintptr) {
	var s linuxSigactiont
	return unsafe.Sizeof(s), unsafe.Offsetof(s.handler), unsafe.Offsetof(s.flags),
		unsafe.Offsetof(s.restorer), unsafe.Offsetof(s.mask)
}
