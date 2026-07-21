// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm && wasm.threads

#include "textflag.h"

// GOWASM=threads: the base atomic operations, implemented with the wasm
// threads proposal's 0xFE instructions (all seq-cst). Call sites are
// usually intrinsified by the compiler; these bodies serve every path
// the intrinsifier cannot see (sync/atomic's assembly trampolines, the
// runtime's linknamed sync/atomic implementations, function values).
// See atomic_wasmthreads.go.

TEXT ·Load(SB), NOSPLIT, $0-12
	Get SP
	I64Load ptr+0(FP)
	I32WrapI64
	I32AtomicLoad $0
	I32Store ret+8(FP)
	RET

TEXT ·Load8(SB), NOSPLIT, $0-9
	Get SP
	I64Load ptr+0(FP)
	I32WrapI64
	I32AtomicLoad8U $0
	I32Store8 ret+8(FP)
	RET

TEXT ·Load64(SB), NOSPLIT, $0-16
	Get SP
	I64Load ptr+0(FP)
	I32WrapI64
	I64AtomicLoad $0
	I64Store ret+8(FP)
	RET

TEXT ·Store(SB), NOSPLIT, $0-12
	I64Load ptr+0(FP)
	I32WrapI64
	I32Load val+8(FP)
	I32AtomicStore $0
	RET

TEXT ·Store8(SB), NOSPLIT, $0-9
	I64Load ptr+0(FP)
	I32WrapI64
	I32Load8U val+8(FP)
	I32AtomicStore8 $0
	RET

TEXT ·Store64(SB), NOSPLIT, $0-16
	I64Load ptr+0(FP)
	I32WrapI64
	I64Load val+8(FP)
	I64AtomicStore $0
	RET

// StorepNoWB(ptr unsafe.Pointer, val unsafe.Pointer)
TEXT ·StorepNoWB(SB), NOSPLIT, $0-16
	I64Load ptr+0(FP)
	I32WrapI64
	I64Load val+8(FP)
	I64AtomicStore $0
	RET

// Xadd returns the NEW value; rmw.add returns the old one, so add the
// delta a second time.
TEXT ·Xadd(SB), NOSPLIT, $0-20
	Get SP
	I64Load ptr+0(FP)
	I32WrapI64
	I32Load delta+8(FP)
	I32AtomicRmwAdd $0
	I32Load delta+8(FP)
	I32Add
	I32Store ret+16(FP)
	RET

TEXT ·Xadd64(SB), NOSPLIT, $0-24
	Get SP
	I64Load ptr+0(FP)
	I32WrapI64
	I64Load delta+8(FP)
	I64AtomicRmwAdd $0
	I64Load delta+8(FP)
	I64Add
	I64Store ret+16(FP)
	RET

TEXT ·Xchg(SB), NOSPLIT, $0-20
	Get SP
	I64Load ptr+0(FP)
	I32WrapI64
	I32Load new+8(FP)
	I32AtomicRmwXchg $0
	I32Store ret+16(FP)
	RET

TEXT ·Xchg8(SB), NOSPLIT, $0-17
	Get SP
	I64Load addr+0(FP)
	I32WrapI64
	I32Load8U v+8(FP)
	I32AtomicRmw8XchgU $0
	I32Store8 ret+16(FP)
	RET

TEXT ·Xchg64(SB), NOSPLIT, $0-24
	Get SP
	I64Load ptr+0(FP)
	I32WrapI64
	I64Load new+8(FP)
	I64AtomicRmwXchg $0
	I64Store ret+16(FP)
	RET

TEXT ·Cas(SB), NOSPLIT, $0-17
	Get SP
	I64Load ptr+0(FP)
	I32WrapI64
	I32Load old+8(FP)
	I32Load new+12(FP)
	I32AtomicRmwCmpxchg $0
	I32Load old+8(FP)
	I32Eq
	I32Store8 ret+16(FP)
	RET

TEXT ·Cas64(SB), NOSPLIT, $0-25
	Get SP
	I64Load ptr+0(FP)
	I32WrapI64
	I64Load old+8(FP)
	I64Load new+16(FP)
	I64AtomicRmwCmpxchg $0
	I64Load old+8(FP)
	I64Eq
	I32Store8 ret+24(FP)
	RET

TEXT ·and8(SB), NOSPLIT, $0-9
	I64Load ptr+0(FP)
	I32WrapI64
	I32Load8U val+8(FP)
	I32AtomicRmw8AndU $0
	Drop
	RET

TEXT ·or8(SB), NOSPLIT, $0-9
	I64Load ptr+0(FP)
	I32WrapI64
	I32Load8U val+8(FP)
	I32AtomicRmw8OrU $0
	Drop
	RET

TEXT ·and(SB), NOSPLIT, $0-12
	I64Load ptr+0(FP)
	I32WrapI64
	I32Load val+8(FP)
	I32AtomicRmwAnd $0
	Drop
	RET

TEXT ·or(SB), NOSPLIT, $0-12
	I64Load ptr+0(FP)
	I32WrapI64
	I32Load val+8(FP)
	I32AtomicRmwOr $0
	Drop
	RET
