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
// ntBootCode is ntBoot with a number after the message, in hex.
//
//go:nosplit
func ntBootCode(msg string, v uintptr) {
	if !iswindows() {
		return
	}
	var line [80]byte
	n := copy(line[:], "nt: ")
	n += copy(line[n:len(line)-20], msg)
	n += copy(line[n:], " 0x")
	for shift := 28; shift >= 0; shift -= 4 {
		d := byte(v>>uint(shift)) & 0xf
		if d < 10 {
			line[n] = '0' + d
		} else {
			line[n] = 'a' + d - 10
		}
		n++
	}
	line[n] = '\n'
	ntwrite1(2, unsafe.Pointer(&line[0]), int32(n+1))
}

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
