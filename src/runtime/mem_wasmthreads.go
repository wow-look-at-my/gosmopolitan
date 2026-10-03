// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build wasm && wasm.threads

package runtime

import _ "unsafe" // for go:linkname

//go:linkname wasmGrowEpoch
var wasmGrowEpoch uint32

// wasmGrowEpochBump records a successful memory.grow. Called by sbrk
// with memlock held.
//
//go:nosplit
func wasmGrowEpochBump() {
	wasmGrowEpoch++
}
