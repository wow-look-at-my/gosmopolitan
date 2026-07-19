// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// stwgc is the GOWASM=threads phase-B3 stop-the-world gate: goroutines
// running allocation-heavy loops AND allocation-free tight loops across
// several worker threads while another goroutine forces runtime.GC()
// repeatedly. Every GC needs cooperative stop-the-world across all
// threads (wasm has no signals): the allocation-free loops are only
// preemptible through the compiler-inserted loop backedge checks, so a
// hang here means the cooperative STW is broken.
//
//	GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o stwgc.wasm ./stwgc
//	GOMAXPROCS=4 node $GOROOT/lib/wasm/wasm_exec_node.js stwgc.wasm
//
// -checklinkname=0: the demo linknames the runtime's M-id hook to prove
// the loops actually ran on >= 3 distinct threads.
package main

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

//go:linkname curMID runtime.wasmThreadsCurMID
func curMID() int64

var (
	stop    atomic.Bool
	midMu   sync.Mutex
	midSeen = map[int64]bool{}
)

func sawM() {
	id := curMID()
	midMu.Lock()
	midSeen[id] = true
	midMu.Unlock()
}

func main() {
	gcs := 40
	if s := os.Getenv("STWGC_CYCLES"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			gcs = n
		}
	}
	println("stwgc: gomaxprocs =", runtime.GOMAXPROCS(0), "forced GCs =", gcs)

	var wg sync.WaitGroup

	// Allocation-heavy loops: keep the GC busy with fresh garbage and a
	// changing live set.
	live := make([][]byte, 64)
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			sawM()
			i := 0
			for !stop.Load() {
				b := make([]byte, 4096)
				b[0] = byte(i)
				live[(g*32+i)%64] = b
				i++
				if i%256 == 0 {
					sawM()
				}
			}
		}(g)
	}

	// Allocation-free tight loops: no calls, no allocation - only the
	// loop backedge checks can stop these for the GC's world stops.
	sink := make([]uint64, 4)
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			sawM()
			x := uint64(g + 1)
			var sum uint64
			for !stop.Load() {
				for i := 0; i < 1<<16; i++ {
					x ^= x << 13
					x ^= x >> 7
					x ^= x << 17
					sum += x
				}
				sawM()
			}
			sink[g] = sum
		}(g)
	}

	// The forcer: repeated full GCs from yet another goroutine.
	for i := 0; i < gcs; i++ {
		runtime.GC()
		if i%10 == 0 {
			println("stwgc: forced GC", i, "done")
		}
	}
	stop.Store(true)
	wg.Wait()

	midMu.Lock()
	distinct := len(midSeen)
	midMu.Unlock()
	println("stwgc: loops ran on", distinct, "distinct Ms")
	if distinct < 3 {
		println("STWGC: FAIL (want >= 3 distinct threads)")
		os.Exit(1)
	}
	// One more GC over the touched heap, then exit cleanly.
	runtime.GC()
	println("STWGC: PASS")
}
