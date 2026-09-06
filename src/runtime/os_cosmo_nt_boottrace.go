// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && cosmontdebug

package runtime

import "unsafe"

// ntBoot prints one boot milestone. The NT boot runs before GODEBUG is
// parsed and before the fd table exists, so the switch is a build tag
// rather than an environment variable: build with -tags cosmontdebug to
// see how far a binary gets before it dies.
//
//go:nosplit
func ntBoot(msg string) {
	if !iswindows() {
		return
	}
	var line [64]byte
	n := copy(line[:], "nt: ")
	n += copy(line[n:len(line)-1], msg)
	line[n] = '\n'
	ntwrite1(2, unsafe.Pointer(&line[0]), int32(n+1))
}
