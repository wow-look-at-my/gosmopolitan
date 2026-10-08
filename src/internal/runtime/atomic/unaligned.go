// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package atomic

// panicUnaligned stays a call. A 64-bit atomic on a 32-bit port inlines into
// the runtime's //go:nowritebarrierrec code, and an inlined panic there is a
// call to gopanic from the runtime itself.
//
//go:noinline
func panicUnaligned() {
	panic("unaligned 64-bit atomic operation")
}
