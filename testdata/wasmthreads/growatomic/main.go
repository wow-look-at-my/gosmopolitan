// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// growatomic is the GOWASM=threads cross-thread grow-observation gate:
// it forces the exact shape of the nondeterministic worker crash at
// runtime.newMarkBits (engine-level stale ATOMIC bounds checks - see
// cmd/internal/obj/wasm's writeGrowEpochGuard and DEBUGGING.md).
//
// Shape: hammer goroutines are pinned to their own worker Ms
// (wasmThreadsRunOnNewM) and NEVER allocate or block - pure atomic
// loads/adds - so nothing ever resynchronizes their instances' cached
// memory size on its own. The main goroutine then repeatedly allocates
// page-crossing chunks (forcing sbrk memory.grow on the main thread) and
// publishes pointers to each chunk's first and last words; the hammers
// immediately perform atomic adds through every published pointer.
// Without the assembler's grow-observation guard, a hammer's first
// atomic touch of any chunk beyond its instance's stale bound traps with
// "RuntimeError: memory access out of bounds"; with the guard, the
// hammer resyncs (memory.grow 0) and the run completes.
//
// GC is disabled so the hammer Ms never allocate GC work (which would
// enter the engine runtime and could refresh their view), and the grower
// waits for the hammers to be pinned and spinning before the first
// grow, so every chunk is born after the hammers went stale.
//
//	GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o growatomic.wasm ./growatomic
//	GOMAXPROCS=4 node $GOROOT/lib/wasm/wasm_exec_node.js growatomic.wasm
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"
	_ "unsafe" // for go:linkname
)

//go:linkname runOnNewM runtime.wasmThreadsRunOnNewM
func runOnNewM(fn func())

//go:linkname curMID runtime.wasmThreadsCurMID
func curMID() int64

const (
	numHammers = 2
	slotCount  = 64
	chunkWords = 32768 // 256 KiB per chunk: every chunk crosses pages
)

var (
	slots    [slotCount * 2]atomic.Pointer[atomic.Uint64]
	pinned   atomic.Int32
	stop     atomic.Bool
	touches  [numHammers]atomic.Uint64
	hammerMs [numHammers]atomic.Int64
	done     [numHammers]chan struct{}
)

// hammer runs pinned to its own M. It must not allocate, block, or call
// into the runtime: only atomic loads of the published slots and atomic
// adds through them, so its instance's memory view is refreshed by
// nothing but the grow-observation guard under test.
func hammer(id int) {
	hammerMs[id].Store(curMID())
	pinned.Add(1)
	var n uint64
	for !stop.Load() {
		for i := range slots {
			if p := slots[i].Load(); p != nil {
				p.Add(1)
				n++
			}
		}
		touches[id].Store(n)
	}
	close(done[id])
}

func main() {
	chunks := 160
	if v := os.Getenv("GROWATOMIC_CHUNKS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			chunks = n
		}
	}

	// No GC: hammer Ms must never run GC work (allocation would enter
	// the engine runtime and could refresh their stale memory view), and
	// the grower's chunks must keep forcing fresh sbrk grows instead of
	// being recycled. ~160 chunks x 256 KiB = 40 MiB, well in budget.
	debug.SetGCPercent(-1)

	fmt.Printf("growatomic: main on M %d, GOMAXPROCS=%d, %d chunks\n",
		curMID(), runtime.GOMAXPROCS(0), chunks)

	for i := 0; i < numHammers; i++ {
		id := i
		done[id] = make(chan struct{})
		go runOnNewM(func() { hammer(id) })
	}
	for pinned.Load() < numHammers {
		time.Sleep(time.Millisecond)
	}
	fmt.Printf("growatomic: hammers pinned on Ms %d and %d, growing...\n",
		hammerMs[0].Load(), hammerMs[1].Load())

	// Grow: every chunk is fresh memory (GC off), first-touched
	// atomically by the stale hammer Ms via the published pointers.
	live := make([][]atomic.Uint64, 0, chunks)
	for i := 0; i < chunks; i++ {
		c := make([]atomic.Uint64, chunkWords)
		live = append(live, c)
		slots[(2*i)%len(slots)].Store(&c[0])
		slots[(2*i+1)%len(slots)].Store(&c[chunkWords-1])
		time.Sleep(2 * time.Millisecond)
	}

	stop.Store(true)
	for i := 0; i < numHammers; i++ {
		<-done[i]
	}

	var sum uint64
	for i := range live {
		sum += live[i][0].Load() + live[i][chunkWords-1].Load()
	}
	t0, t1 := touches[0].Load(), touches[1].Load()
	fmt.Printf("growatomic: hammer touches: %d + %d, chunk-end adds: %d\n", t0, t1, sum)
	if t0 == 0 || t1 == 0 || sum == 0 {
		// The gate is only a gate if the hammers really touched fresh
		// chunks from their own Ms.
		fmt.Println("growatomic: FAIL (a hammer never touched a published chunk)")
		os.Exit(1)
	}
	fmt.Println("GROWATOMIC: PASS")
}
