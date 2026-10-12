// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

// WebAssembly has no extra Ms: it has no cgo, and no thread it did not start
// calls into Go, so nothing ever sleeps in lockextra.

//go:nosplit
func extraMSemaSleep() {
	throw("lockextra sleeps on wasm")
}

//go:nosplit
func extraMSemaWake(count uint32) {
	throw("lockextra wakes on wasm")
}
