// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// speedup is the GOWASM=threads phase-B3 parallelism gate: a CPU-bound
// workload split into shards run as ordinary goroutines. With
// GOMAXPROCS>1 (unclamped under GOWASM=threads) the scheduler spreads
// the shards across the main thread and the worker pool; the driver
// compares wall-clock time against a GOMAXPROCS=1 run of the same
// binary and checks that the checksums are identical.
//
//	GOOS=js GOARCH=wasm GOWASM=threads go build -o speedup.wasm ./speedup
//	GOMAXPROCS=1 node $GOROOT/lib/wasm/wasm_exec_node.js speedup.wasm
//	GOMAXPROCS=4 node $GOROOT/lib/wasm/wasm_exec_node.js speedup.wasm
//
// Only println is used for output: with GOMAXPROCS>1 any goroutine
// (including main) can land on a worker M, where syscall/js - and
// with it fmt to os.Stdout - is unavailable in this phase.
package main

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
	_ "unsafe" // for go:linkname
)

//go:linkname curMID runtime.wasmThreadsCurMID
func curMID() int64

const shards = 4

func shardWork(seed uint64, iters int) uint64 {
	x := seed
	var sum uint64
	for i := 0; i < iters; i++ {
		// xorshift64 keeps the loop free of calls: only the
		// compiler-inserted backedge checks can preempt it.
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		sum += x
	}
	return sum
}

func main() {
	iters := 120_000_000
	if s := os.Getenv("SPEEDUP_ITERS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			iters = n
		}
	}

	procs := runtime.GOMAXPROCS(0)
	println("speedup: gomaxprocs =", procs, "shards =", shards, "iters/shard =", iters)

	var (
		wg      sync.WaitGroup
		results [shards]uint64
		mids    [shards]int64
		midsEnd [shards]int64
	)
	start := time.Now()
	for i := 0; i < shards; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mids[i] = curMID()
			results[i] = shardWork(uint64(i)+0x9E3779B97F4A7C15, iters)
			midsEnd[i] = curMID()
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	var combined uint64
	for i, r := range results {
		combined ^= r + uint64(i)
	}
	distinct := map[int64]bool{}
	for i := range mids {
		distinct[midsEnd[i]] = true
		println("speedup: shard", i, "started on M", mids[i], "finished on M", midsEnd[i])
	}
	println("speedup: shards finished on", len(distinct), "distinct Ms")
	println("speedup: elapsed_ms =", int64(elapsed/time.Millisecond))
	println("speedup: checksum =", combined)
	println("SPEEDUP: DONE")
}
