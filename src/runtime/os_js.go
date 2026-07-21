// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package runtime

import (
	"unsafe"
)

func exit(code int32)

func write1(fd uintptr, p unsafe.Pointer, n int32) int32 {
	if fd > 2 {
		throw("runtime.write to fd > 2 is unsupported")
	}
	wasmWrite(fd, p, n)
	return n
}

//go:wasmimport gojs runtime.wasmWrite
//go:noescape
func wasmWrite(fd uintptr, p unsafe.Pointer, n int32)

func usleep(usec uint32) {
	if wasmThreadsEnabled {
		// Timed futex sleep (see os_wasmthreads.go). Without threads
		// there is nothing that could run in the meantime anyway.
		wasmThreadsUsleep(usec)
	}
}

// syscall_wasmWrite lets package syscall write to stdout/stderr through
// the runtime's raw write import. Under GOWASM=threads the import is
// implemented on worker threads too (unlike syscall/js), so fmt and os
// printing keep working from goroutines running on worker Ms.
//
//go:linkname syscall_wasmWrite syscall.runtime_wasmWrite
func syscall_wasmWrite(fd uintptr, p unsafe.Pointer, n int32) {
	wasmWrite(fd, p, n)
}

// syscall_onWorkerThread reports whether the caller runs on a worker
// -thread M under GOWASM=threads (always false without it).
//
//go:linkname syscall_onWorkerThread syscall.runtime_onWorkerThread
func syscall_onWorkerThread() bool {
	return wasmThreadsEnabled && getg().m != &m0
}

//go:wasmimport gojs runtime.getRandomData
//go:noescape
func getRandomData(r []byte)

func readRandom(r []byte) int {
	getRandomData(r)
	return len(r)
}

func goenvs() {
	goenvs_unix()
}
