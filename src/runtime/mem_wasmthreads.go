// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm && wasm.threads

package runtime

import _ "unsafe" // for go:linkname

// wasmGrowEpoch counts successful memory.grow calls. sbrk bumps it under
// memlock, immediately after the grow and before the grown memory can be
// published to any other thread. It lives in static data - within the
// initial linear memory - so loading it is in bounds from every instance
// no matter how stale that instance's memory-size view is.
//
// Consumer: the grow-observation guard the assembler emits before EVERY
// atomic memory access under GOWASM=threads (writeGrowEpochGuard in
// cmd/internal/obj/wasm). Engines give atomic accesses an explicit
// bounds check against a per-instance cached memory size that lags
// cross-thread grows, so without a resync an atomic access to
// correctly-published fresh memory can trap even though plain accesses
// to the same address succeed (those go through guard pages backed by
// truly-committed memory). The guard compares this counter against a
// per-instance "observed epoch" wasm global and executes memory.grow 0
// (which resynchronizes the instance's cached size) on mismatch.
//
// A plain, non-atomic store under memlock is correct here: any thread
// that can legitimately hold a pointer into the grown region obtained it
// through a synchronizing atomic operation that happens-after this bump
// (sbrk has not even returned the pointer when it bumps), and the guard
// reads the epoch after that synchronization point, so happens-before
// delivers the bumped value. A racing early read that misses the bump
// can only belong to a thread that cannot yet hold such a pointer, and
// its stale-but-in-bounds accesses are unaffected.
//
// uint32 cannot wrap: the memory maximum is 32768 pages and every
// successful grow adds at least one page, so a process sees at most
// ~32768 bumps.
//
// The bare linkname blesses the cross-package references: the guard is
// emitted into every package that uses atomics, and the linker's
// checkLinkname requires the definition side of such a reference to be
// push-linknamed.
//
//go:linkname wasmGrowEpoch
var wasmGrowEpoch uint32

// wasmGrowEpochBump records a successful memory.grow. Called by sbrk
// with memlock held.
//
//go:nosplit
func wasmGrowEpochBump() {
	wasmGrowEpoch++
}
