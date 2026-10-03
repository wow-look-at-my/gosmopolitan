// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm && !wasm.threads

package runtime

// Without GOWASM=threads there is one thread and one instance, so there
// is no cross-instance grow observation to maintain (and no
// wasmGrowEpoch: the assembler emits no grow-observation guards - see
// mem_wasmthreads.go for the threads version).
//
//go:nosplit
func wasmGrowEpochBump() {}
