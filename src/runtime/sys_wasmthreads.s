// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm && wasm.threads

#include "go_asm.h"
#include "textflag.h"

// GOWASM=threads primitives over the wasm threads proposal's 0xFE
// instructions, plus the worker-thread entry point.

// There is no way to yield the hardware thread from inside wasm; the
// futex-based lock falls through to a real futexsleep quickly, so a plain
// return is fine here.
TEXT runtime·osyield(SB), NOSPLIT, $0-0
	RET

// publicationBarrier: a store-store barrier so that an object's
// initialized contents are visible to other threads before the pointer
// that publishes it. atomic.fence (0xFE 0x03) is a full seq-cst fence,
// which subsumes it.
TEXT ·publicationBarrier(SB), NOSPLIT, $0-0
	AtomicFence
	RET

// func futexsleep(addr *uint32, val uint32, ns int64)
// Blocks while *addr == val, for at most ns nanoseconds (ns < 0: forever),
// via memory.atomic.wait32. The result (0 woken / 1 value mismatch /
// 2 timed out) is discarded: callers re-check their condition in a loop.
TEXT runtime·futexsleep(SB), NOSPLIT, $0-24
	I64Load addr+0(FP)
	I32WrapI64
	I32Load val+8(FP)
	I64Load ns+16(FP)
	MemoryAtomicWait32 $0
	Drop
	RET

// func futexwakeup(addr *uint32, cnt uint32)
// Wakes at most cnt waiters blocked on addr via memory.atomic.notify.
TEXT runtime·futexwakeup(SB), NOSPLIT, $0-12
	I64Load addr+0(FP)
	I32WrapI64
	I32Load cnt+8(FP)
	MemoryAtomicNotify $0
	Drop
	RET

// wasm_export_thread_run is the entry point of a pool worker thread. It
// gets called (exported as "wasm_thread_run") by the worker-side host
// shim (lib/wasm/wasm_exec_worker.js) on a fresh instance of the module
// sharing the main instance's linear memory, and never returns.
//
// It does NOT follow the Go ABI. One WebAssembly parameter:
// R0: worker id (i32, diagnostics only)
//
// The worker parks in a pure-wasm futex wait on the spawn mailbox (no Go
// stack, no g - the Go runtime may not even be initialized yet). When
// newosproc posts an M (see os_wasmthreads.go for the mailbox protocol),
// the worker claims it, points its per-instance SP and g globals at the
// M's g0, and enters mstart. mstart never returns; like wasm_export_run,
// the first goroutine switch unwinds the wasm stack and wasm_pc_f_loop
// takes over. The loop only exits when PAUSE is set, which on a worker
// can only happen via runtime.exit (the wasmExit import tears the process
// down first), so falling out of it traps.
TEXT wasm_export_thread_run(SB),NOSPLIT,$0
	// R0: worker id (i32, unused so far)
	// R1, R2: i32 scratch; R3, R4: i64 scratch (see cmd/internal/obj/wasm).

	// R1 = &runtime.wasmSpawnState
	MOVD $runtime·wasmSpawnState(SB), R3
	Get R3
	I32WrapI64
	Set R1

	// Wait for a posted M (state == 2) and claim it (state = 3).
	Block
		Loop
			Get R1
			I32Const $2
			I32Const $3
			I32AtomicRmwCmpxchg $0
			Tee R2
			I32Const $2
			I32Eq
			BrIf $1 // claimed

			// Not posted: sleep until state changes from what we saw.
			Get R1
			Get R2
			I64Const $-1
			MemoryAtomicWait32 $0
			Drop
			Br $0
		End
	End

	// R3 = mp (runtime.wasmSpawnMP)
	MOVD $runtime·wasmSpawnMP(SB), R3
	Get R3
	I32WrapI64
	I64Load $0
	Set R3

	// Release the mailbox (state = 0) and wake anyone waiting for it.
	Get R1
	I32Const $0
	I32AtomicStore $0
	Get R1
	I32Const $-1
	MemoryAtomicNotify $0
	Drop

	// Acknowledge the claim: wasmSpawnSeq++ and wake the poster.
	MOVD $runtime·wasmSpawnSeq(SB), R4
	Get R4
	I32WrapI64
	Set R2
	Get R2
	I32Const $1
	I32AtomicRmwAdd $0
	Drop
	Get R2
	I32Const $-1
	MemoryAtomicNotify $0
	Drop

	// R4 = mp.g0
	I64Load m_g0(R3)
	Set R4

	// SP = mp.g0.sched.sp (set up by newosproc: top of the g0 stack)
	I64Load (g_sched+gobuf_sp)(R4)
	I32WrapI64
	Set SP

	// g = mp.g0
	Get R4
	Set g

	// Run the M's schedule loop; see wasm_export_run for the pattern.
	I32Const $0 // entry PC_B
	Call runtime·mstart(SB)
	Drop
	Call wasm_pc_f_loop(SB)

	// Only reachable if PAUSE was set on this instance, which on a worker
	// only runtime.exit does - and its wasmExit import does not return.
	UNDEF
