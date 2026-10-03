// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build js && wasm && wasm.threads

package runtime

var WasmThreadsRunOnNewM = wasmThreadsRunOnNewM

func WasmThreadsCurMID() int64 {
	return wasmThreadsCurMID()
}

var WasmThreadsIdleWorkerMs = wasmThreadsIdleWorkerMs

// WasmThreadsMCount returns the number of Ms ever created (Ms never exit on
// wasm), read under sched.lock.
func WasmThreadsMCount() int32 {
	lock(&sched.lock)
	n := mcount()
	unlock(&sched.lock)
	return n
}
