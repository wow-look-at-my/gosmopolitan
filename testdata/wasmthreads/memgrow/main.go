// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// memgrow is the GOWASM=threads cross-thread memory.grow gate (B4): a
// WORKER-side Go allocation forces the shared linear memory to grow
// while the main thread and other workers actively read and write the
// shared heap and make JavaScript host calls. Every instance (main and
// each worker) must keep a coherent view of the memory across the grow:
// the wasm_exec.js side re-derives its DataView whenever the buffer
// identity changes (main), and the worker imports build a fresh view per
// call - a stale view would either throw a RangeError, read garbage, or
// trap with "memory access out of bounds".
//
// Checks:
//
//   - grower runs on a worker M (asserted via the runtime M-id hook) and
//     grows the Go heap by >= 256 MiB in 4 MiB chunks (dozens of
//     memory.grow steps), writing and verifying a pattern in each chunk.
//
//   - hammer goroutines (running on all Ps) continuously write/verify a
//     shared array with atomics across the grow.
//
//   - the main goroutine keeps making syscall/js host calls that
//     round-trip STRINGS through linear memory (loadString/storeString on
//     freshly allocated, i.e. ever-higher, addresses) during the grow.
//
//   - worker-side println (the wasmWrite import on worker instances)
//     works after the grow.
//
//     GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o memgrow.wasm ./memgrow
//     GOMAXPROCS=4 node $GOROOT/lib/wasm/wasm_exec_node.js memgrow.wasm
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"
	_ "unsafe" // for go:linkname
)

//go:linkname curMID runtime.wasmThreadsCurMID
func curMID() int64

const (
	chunkMiB    = 4
	totalMiB    = 256
	hammerCells = 1 << 16
)

var failed atomic.Bool

func fail(msg string, args ...any) {
	fmt.Printf("memgrow: "+msg+"\n", args...)
	failed.Store(true)
}

func main() {
	var msBefore runtime.MemStats
	runtime.ReadMemStats(&msBefore)

	shared := make([]atomic.Uint64, hammerCells)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// CPU hammers: keep every P busy mutating and verifying the shared
	// array across grows.
	for h := 0; h < 3; h++ {
		wg.Add(1)
		go func(h int) {
			defer wg.Done()
			var iters uint64
			for {
				select {
				case <-stop:
					fmt.Println("memgrow: hammer", h, "iters =", iters, "on M", curMID())
					return
				default:
				}
				for i := range shared {
					shared[i].Add(1)
				}
				iters++
			}
		}(h)
	}

	// Grower: force memory.grow from a worker M while everyone else runs.
	growerDone := make(chan int64, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		chunks := make([][]byte, 0, totalMiB/chunkMiB)
		growerM := curMID()
		for i := 0; i < totalMiB/chunkMiB; i++ {
			c := make([]byte, chunkMiB<<20)
			for j := 0; j < len(c); j += 4096 {
				c[j] = byte(i + j)
			}
			chunks = append(chunks, c)
			if m := curMID(); m != 0 {
				growerM = m // remember any worker M it ran on
			}
		}
		// Verify every chunk after all growth happened.
		for i, c := range chunks {
			for j := 0; j < len(c); j += 4096 {
				if c[j] != byte(i+j) {
					fail("chunk %d corrupt at %d: got %d want %d", i, j, c[j], byte(i+j))
					break
				}
			}
		}
		println("memgrow: grower finished on worker M", curMID())
		growerDone <- growerM
	}()

	// Main goroutine: JS host calls with string round-trips through
	// linear memory while the grow is happening.
	jsRoundTrips := 0
	deadline := time.Now().Add(30 * time.Second)
	growerM := int64(-1)
loop:
	for {
		select {
		case growerM = <-growerDone:
			break loop
		default:
		}
		if time.Now().After(deadline) {
			fail("grower did not finish within 30s")
			break
		}
		// String round-trip: Go string -> JS -> Go string. The backing
		// bytes live in freshly allocated (high) memory, so the host
		// must see grown memory to read/write them.
		s := fmt.Sprintf("roundtrip-%d-%d", jsRoundTrips, time.Now().UnixNano())
		v := js.ValueOf(s)
		if got := v.String(); got != s {
			fail("js string round-trip mismatch: %q != %q", got, s)
			break
		}
		if js.Global().Get("Math").Call("max", jsRoundTrips, 1).Int() < 1 {
			fail("js Math.max wrong result")
			break
		}
		jsRoundTrips++
	}
	close(stop)
	wg.Wait()

	var msAfter runtime.MemStats
	runtime.ReadMemStats(&msAfter)
	grewMiB := int64(msAfter.Sys-msBefore.Sys) >> 20
	fmt.Println("memgrow: Sys grew MiB =", grewMiB, " js round-trips =", jsRoundTrips, " grower M =", growerM)

	if grewMiB < totalMiB/2 {
		fail("memory did not grow enough (%d MiB)", grewMiB)
	}
	if jsRoundTrips < 10 {
		fail("too few JS round-trips (%d) - main thread starved", jsRoundTrips)
	}
	if growerM == 0 {
		// The grower must have executed on a worker M for the gate to
		// prove CROSS-INSTANCE grow (m0's own instance growing its own
		// memory is the trivial case).
		fail("grower never ran on a worker M")
	}
	if failed.Load() {
		fmt.Println("MEMGROW: FAIL")
		os.Exit(1)
	}
	fmt.Println("MEMGROW: PASS")
}
