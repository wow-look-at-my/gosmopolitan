// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// threaddemo is the GOWASM=threads phase-B2 gate: real Go code running
// on worker threads (worker Ms), sharing one heap with the main
// instance.
//
// Build and run (the pool is spawned automatically by wasm_exec_node.js):
//
//	GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o threaddemo.wasm ./threaddemo
//	node $GOROOT/lib/wasm/wasm_exec_node.js threaddemo.wasm
//
// -checklinkname=0 is needed for the runtime test hooks below.
//
// What it demonstrates:
//   - runtime.newosproc handing new Ms to pre-spawned pool workers
//     (wasmThreadsRunOnNewM pins the calling goroutine to its M, so the
//     scheduler must move the P - and the spawned goroutine - to another
//     M: first organically via stoplockedm/handoffp/startm/newosproc,
//     then, for the third spawn, reusing a parked worker M via mget);
//   - shared-heap visibility in both directions with proper
//     synchronization: buffers written on one thread and checksummed on
//     another, a sync.Mutex-protected counter bumped from three
//     different Ms, and channel sends/receives crossing Ms (backed by
//     the new futex locks/notes over memory.atomic.wait32/notify);
//   - a nested spawn: the worker's goroutine itself pins and spawns,
//     so TWO worker Ms exist concurrently (plus the parked main M);
//   - runtime.GC() afterwards, over the heap those threads touched;
//   - clean completion with exit code 0.
//
// Worker Ms must not call syscall/js (JavaScript values live on the main
// thread; host-call forwarding is a later phase), so the code that runs
// on worker Ms sticks to pure Go plus println (the raw runtime.wasmWrite
// import, which the worker host shim implements).
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	_ "unsafe" // for go:linkname
)

// Runtime test/demo hooks for GOWASM=threads phase B2 (see
// runtime/os_wasmthreads.go).
//
//go:linkname runOnNewM runtime.wasmThreadsRunOnNewM
func runOnNewM(fn func())

//go:linkname curMID runtime.wasmThreadsCurMID
func curMID() int64

func checksum(b []byte) uint32 {
	// FNV-1a.
	h := uint32(2166136261)
	for _, c := range b {
		h ^= uint32(c)
		h *= 16777619
	}
	return h
}

func main() {
	mainM := curMID()
	fmt.Printf("threaddemo: main goroutine on M %d (main thread)\n", mainM)

	// A heap buffer written by the main thread, to be read (and
	// mutated) by the worker Ms.
	buf := make([]byte, 1<<20)
	for i := range buf {
		buf[i] = byte(i*7 + i>>9)
	}
	mainSum := checksum(buf)
	fmt.Printf("threaddemo: buffer checksum on main M: %#08x\n", mainSum)

	var mu sync.Mutex
	counter := 0
	bump := func(n int) {
		for i := 0; i < n; i++ {
			mu.Lock()
			counter++
			mu.Unlock()
		}
	}

	msgs := make(chan string, 16)
	var workerM, nestedM int64
	var workerSum, nestedSum uint32

	runOnNewM(func() {
		workerM = curMID()
		println("threaddemo: [worker] real Go code on M", workerM, "(worker thread)")
		workerSum = checksum(buf) // reads the heap the main thread wrote
		for i := range buf {
			buf[i] ^= 0xA5 // and writes it back for main to verify
		}
		bump(1000)
		msgs <- fmt.Sprintf("worker M %d: checksum %#08x", workerM, workerSum)

		// Nested spawn: pin this goroutine to ITS M and spawn again, so
		// a second worker M runs concurrently with this (parked) one.
		runOnNewM(func() {
			nestedM = curMID()
			println("threaddemo: [nested] real Go code on M", nestedM, "(second worker thread)")
			nestedSum = checksum(buf) // sees the worker M's mutation
			bump(1000)
			msgs <- fmt.Sprintf("nested M %d: checksum %#08x", nestedM, nestedSum)
		})
	})
	bump(1000)

	// Third spawn: both worker Ms are parked now; the scheduler reuses
	// one (mget) instead of taking a fresh pool worker.
	var thirdM int64
	runOnNewM(func() {
		thirdM = curMID()
		msgs <- fmt.Sprintf("third spawn ran on reused M %d", thirdM)
	})

	fmt.Printf("threaddemo: back on main M %d\n", curMID())
	for i := 0; i < 3; i++ {
		fmt.Println("threaddemo: channel from worker:", <-msgs)
	}

	fail := false
	check := func(ok bool, what string) {
		if ok {
			fmt.Println("threaddemo: ok:", what)
		} else {
			fmt.Println("threaddemo: FAIL:", what)
			fail = true
		}
	}
	mutated := checksum(buf)
	check(curMID() == mainM, fmt.Sprintf("main goroutine still on M %d", mainM))
	check(workerM != mainM, fmt.Sprintf("worker ran on a different M (%d != %d)", workerM, mainM))
	check(nestedM != mainM && nestedM != workerM, fmt.Sprintf("nested worker ran on a third M (%d)", nestedM))
	check(thirdM == workerM || thirdM == nestedM, fmt.Sprintf("third spawn reused a parked worker M (%d)", thirdM))
	check(workerSum == mainSum, "worker M read the main thread's buffer intact")
	check(nestedSum == mutated, "nested M saw the worker M's mutation; main sees the same bytes")
	check(nestedSum != mainSum, "the mutation actually changed the checksum")
	mu.Lock()
	c := counter
	mu.Unlock()
	check(c == 3000, fmt.Sprintf("mutex counter bumped from 3 Ms: %d == 3000", c))

	runtime.GC() // over the heap those threads touched

	if fail {
		fmt.Println("THREADDEMO: FAIL")
		os.Exit(1)
	}
	fmt.Println("THREADDEMO: PASS")
}
