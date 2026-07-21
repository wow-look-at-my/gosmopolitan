// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime

var WasmThreadsRunOnNewM = wasmThreadsRunOnNewM

func WasmThreadsCurMID() int64 {
	return wasmThreadsCurMID()
}

var WasmThreadsIdleWorkerMs = wasmThreadsIdleWorkerMs

// WasmThreadsMCount returns the number of Ms ever created (Ms never
// exit on wasm), read under sched.lock. Growth across a spawn means a
// fresh pool worker was claimed instead of a parked M being reused.
func WasmThreadsMCount() int32 {
	lock(&sched.lock)
	n := mcount()
	unlock(&sched.lock)
	return n
}
