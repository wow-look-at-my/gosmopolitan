// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm

package runtime

// pause is only used on wasm.
func pause(newsp uintptr) { panic("unreachable") }

// GOWASM=threads (js/wasm only) hooks referenced from shared scheduler
// code; see os_wasmthreads.go for the real implementations. With the
// constant false every use is dead-code eliminated.
const wasmThreadsEnabled = false

func wasmClampGOMAXPROCS(n int32) int32 { return 1 }

func wasmMaxMCount() int32 { return 0x7fffffff }

//go:nosplit
func wasmWakeMainThread() {}

//go:nosplit
func wasmMainMParkedInEventLoop() bool { return false }

//go:nosplit
func wasmSchedNudgeWake() {}

func wasmThreadsPidleput(pp *p) {}

func wasmCheckdeadDump() {}
