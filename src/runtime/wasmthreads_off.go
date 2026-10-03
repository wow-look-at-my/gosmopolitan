// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build wasm && !(js && wasm.threads)

package runtime

// GOWASM=threads is off (or the target is wasip1, which the linker rejects in combination with GOWASM=threads).

const wasmThreadsEnabled = false

func wasmThreadsNewosproc(mp *m) {
	throw("newosproc: not implemented")
}

//go:nosplit
func wasmThreadsUsleep(usec uint32) {
}

// wasmClampGOMAXPROCS: without threads only one CPU is possible.
func wasmClampGOMAXPROCS(n int32) int32 {
	return 1
}

func wasmMaxMCount() int32 {
	return 0x7fffffff // unreachable: startm's budget check is threads-only
}

//go:nosplit
func wasmWakeMainThread() {
}

//go:nosplit
func wasmMainMParkedInEventLoop() bool {
	return false
}

//go:nosplit
func wasmSchedNudgeWake() {
}

func wasmThreadsPidleput(pp *p) {
}

func wasmCheckdeadDump() {
}
