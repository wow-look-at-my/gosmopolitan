// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm && wasm.threads

// GOWASM=threads: real atomic operations over the wasm threads
// proposal's 0xFE instructions.
//
// Call sites of the base operations (Load, Store, Xadd, Xchg, Cas and
// their 8/64-bit variants) are usually intrinsified by the compiler into
// inline atomic instructions, but the function BODIES must be genuinely
// atomic too: sync/atomic's assembly trampolines (asm.s) jump here, the
// runtime's atomic_pointer.go reaches SwapUintptr/CompareAndSwapUintptr/
// StoreUintptr through linknamed declarations that the intrinsifier does
// not see, and taking a function's address always yields the real body.
// Without GOWASM=threads the plain non-atomic bodies (atomic_wasm.go)
// remain correct because there is only one thread.
//
// The base operations are implemented in assembly
// (atomic_wasmthreads.s); the derived spellings below delegate to them
// (those calls are themselves intrinsified where possible, and land on
// the atomic assembly otherwise).

// linkname.go exports this package's functions to sync/atomic's assembly
// with //go:linknamestd. A second //go:linkname here for one of those names
// is a duplicate and does not compile.

package atomic

import "unsafe"

// Base operations, implemented in atomic_wasmthreads.s with 0xFE atomic
// instructions (all seq-cst).

//go:noescape
func Load(ptr *uint32) uint32

//go:noescape
func Load8(ptr *uint8) uint8

//go:noescape
func Load64(ptr *uint64) uint64

//go:noescape
func Store(ptr *uint32, val uint32)

//go:noescape
func Store8(ptr *uint8, val uint8)

//go:noescape
func Store64(ptr *uint64, val uint64)

//go:noescape
func Xadd(ptr *uint32, delta int32) uint32

//go:noescape
func Xadd64(ptr *uint64, delta int64) uint64

//go:noescape
func Xchg(ptr *uint32, new uint32) uint32

//go:noescape
func Xchg8(addr *uint8, v uint8) uint8

//go:noescape
func Xchg64(ptr *uint64, new uint64) uint64

//go:noescape
func Cas(ptr *uint32, old, new uint32) bool

//go:noescape
func Cas64(ptr *uint64, old, new uint64) bool

// StorepNoWB performs *ptr = val atomically and without a write
// barrier.
//
// NO go:noescape annotation; see atomic_pointer.go.
func StorepNoWB(ptr unsafe.Pointer, val unsafe.Pointer)

// Derived spellings.

//go:nosplit
func Loadp(ptr unsafe.Pointer) unsafe.Pointer {
	return unsafe.Pointer(uintptr(Load64((*uint64)(ptr))))
}

//go:nosplit
func LoadAcq(ptr *uint32) uint32 {
	return Load(ptr)
}

//go:nosplit
func LoadAcq64(ptr *uint64) uint64 {
	return Load64(ptr)
}

//go:nosplit
func LoadAcquintptr(ptr *uintptr) uintptr {
	return uintptr(Load64((*uint64)(unsafe.Pointer(ptr))))
}

//go:nosplit
func Xadduintptr(ptr *uintptr, delta uintptr) uintptr {
	return uintptr(Xadd64((*uint64)(unsafe.Pointer(ptr)), int64(delta)))
}

//go:nosplit
func Xchgint32(ptr *int32, new int32) int32 {
	return int32(Xchg((*uint32)(unsafe.Pointer(ptr)), uint32(new)))
}

//go:nosplit
func Xchgint64(ptr *int64, new int64) int64 {
	return int64(Xchg64((*uint64)(unsafe.Pointer(ptr)), uint64(new)))
}

//go:nosplit
func Xchguintptr(ptr *uintptr, new uintptr) uintptr {
	return uintptr(Xchg64((*uint64)(unsafe.Pointer(ptr)), uint64(new)))
}

//go:nosplit
func And8(ptr *uint8, val uint8) {
	and8(ptr, val)
}

//go:nosplit
func Or8(ptr *uint8, val uint8) {
	or8(ptr, val)
}

// NOTE: Do not add atomicxor8 (XOR is not idempotent).

//go:nosplit
func And(ptr *uint32, val uint32) {
	and(ptr, val)
}

//go:nosplit
func Or(ptr *uint32, val uint32) {
	or(ptr, val)
}

// and8, or8, and, or are the assembly rmw.and/rmw.or primitives; the
// exported And8/Or8/And/Or wrappers exist because the compiler
// intrinsifies calls to the exported names on wasm, and an intrinsic
// must not be its own body.
//
//go:noescape
func and8(ptr *uint8, val uint8)

//go:noescape
func or8(ptr *uint8, val uint8)

//go:noescape
func and(ptr *uint32, val uint32)

//go:noescape
func or(ptr *uint32, val uint32)

//go:nosplit
func StoreRel(ptr *uint32, val uint32) {
	Store(ptr, val)
}

//go:nosplit
func StoreRel64(ptr *uint64, val uint64) {
	Store64(ptr, val)
}

//go:nosplit
func StoreReluintptr(ptr *uintptr, val uintptr) {
	Store64((*uint64)(unsafe.Pointer(ptr)), uint64(val))
}

//go:nosplit
func Casint32(ptr *int32, old, new int32) bool {
	return Cas((*uint32)(unsafe.Pointer(ptr)), uint32(old), uint32(new))
}

//go:nosplit
func Casint64(ptr *int64, old, new int64) bool {
	return Cas64((*uint64)(unsafe.Pointer(ptr)), uint64(old), uint64(new))
}

//go:nosplit
func Casp1(ptr *unsafe.Pointer, old, new unsafe.Pointer) bool {
	return Cas64((*uint64)(unsafe.Pointer(ptr)), uint64(uintptr(old)), uint64(uintptr(new)))
}

//go:nosplit
func Casuintptr(ptr *uintptr, old, new uintptr) bool {
	return Cas64((*uint64)(unsafe.Pointer(ptr)), uint64(old), uint64(new))
}

//go:nosplit
func CasRel(ptr *uint32, old, new uint32) bool {
	return Cas(ptr, old, new)
}

//go:nosplit
func Storeint32(ptr *int32, new int32) {
	Store((*uint32)(unsafe.Pointer(ptr)), uint32(new))
}

//go:nosplit
func Storeint64(ptr *int64, new int64) {
	Store64((*uint64)(unsafe.Pointer(ptr)), uint64(new))
}

//go:nosplit
func Storeuintptr(ptr *uintptr, new uintptr) {
	Store64((*uint64)(unsafe.Pointer(ptr)), uint64(new))
}

//go:nosplit
func Loaduintptr(ptr *uintptr) uintptr {
	return uintptr(Load64((*uint64)(unsafe.Pointer(ptr))))
}

//go:nosplit
func Loaduint(ptr *uint) uint {
	return uint(Load64((*uint64)(unsafe.Pointer(ptr))))
}

//go:nosplit
func Loadint32(ptr *int32) int32 {
	return int32(Load((*uint32)(unsafe.Pointer(ptr))))
}

//go:nosplit
func Loadint64(ptr *int64) int64 {
	return int64(Load64((*uint64)(unsafe.Pointer(ptr))))
}

//go:nosplit
func Xaddint32(ptr *int32, delta int32) int32 {
	return int32(Xadd((*uint32)(unsafe.Pointer(ptr)), delta))
}

//go:nosplit
func Xaddint64(ptr *int64, delta int64) int64 {
	return int64(Xadd64((*uint64)(unsafe.Pointer(ptr)), delta))
}
