// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	_ "unsafe" // for linkname
)

// runtimeGetAuxv is the accessor x/sys/cpu and x/sys/unix reach by the same
// linkname. Reading it the way they do is the point of this check.
//
//go:linkname runtimeGetAuxv runtime.getAuxv
func runtimeGetAuxv() []uintptr

// checkAuxv asserts the process has an auxv. An empty one is not merely
// missing information: x/sys/cpu answers it by reading the aarch64 ID
// registers, and that instruction is privileged on Darwin, so a binary
// linking anything under it dies of SIGILL in a package init before main.
// The chain that found this was go-git -> ProtonMail/go-crypto ->
// cloudflare/circl -> x/sys/cpu, and nothing in that chain is optional.
func checkAuxv() {
	av := runtimeGetAuxv()
	if len(av) == 0 {
		fail("auxv", "empty on %s; x/sys/cpu reads the ID registers instead and SIGILLs", cosmoHostOS())
		return
	}
	if len(av)%2 != 0 {
		fail("auxv", "%d entries, want (tag, value) pairs", len(av))
		return
	}
	ok("auxv", fmt.Sprintf("%d pairs on %s", len(av)/2, cosmoHostOS()))
}
