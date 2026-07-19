// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm && !(js && wasm.threads)

package runtime

// GOWASM=threads is off (or the target is wasip1, which the linker
// rejects in combination with GOWASM=threads): there are no threads.
// See os_wasmthreads.go for the real implementations.

const wasmThreadsEnabled = false

func wasmThreadsNewosproc(mp *m) {
	throw("newosproc: not implemented")
}

//go:nosplit
func wasmThreadsUsleep(usec uint32) {
}
