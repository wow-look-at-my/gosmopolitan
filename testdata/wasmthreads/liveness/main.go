// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// liveness is the GOWASM=threads phase-B3 event-loop liveness gate:
// while four worker threads are CPU-busy for ~2 seconds, the main
// thread's JavaScript event loop must stay live - a JS setTimeout fires
// on schedule (pure JS, timestamped) - and a Go timer goroutine must
// fire close to its deadline even though every P is running a tight
// loop (the armed loop backedge checks yield to the scheduler).
//
// Placement: the main goroutine arms the JS timer via syscall/js (on the
// main thread), then calls the runtime's RunOnNewM hook, which locks it
// to the main M and forces the worker function onto a worker thread. The
// worker function spawns the CPU shards and the Go timer goroutine, so
// with GOMAXPROCS=4 all four Ps end up on pool workers while the main M
// parks - in the event loop, which is exactly what phase B3 adds.
//
//	GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o liveness.wasm ./liveness
//	GOMAXPROCS=4 node $GOROOT/lib/wasm/wasm_exec_node.js liveness.wasm
package main

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"
	_ "unsafe" // for go:linkname
)

//go:linkname runOnNewM runtime.wasmThreadsRunOnNewM
func runOnNewM(fn func())

//go:linkname curMID runtime.wasmThreadsCurMID
func curMID() int64

const (
	cpuGoroutines = 4
	jsTimerMS     = 150
	goTimerMS     = 200
)

var (
	goTimerDelayMS atomic.Int64
	busyMS         = 2000
)

func worker() {
	// Runs on a worker M. Spawn the CPU shards and the Go timer
	// goroutine from here so none of them starts on the (locked) main M.
	var wg sync.WaitGroup

	timerStart := time.Now()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-time.After(goTimerMS * time.Millisecond)
		goTimerDelayMS.Store(int64(time.Since(timerStart) / time.Millisecond))
	}()

	deadline := timerStart.Add(time.Duration(busyMS) * time.Millisecond)
	mids := make([]int64, cpuGoroutines)
	for g := 0; g < cpuGoroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			mids[g] = curMID()
			x := uint64(g + 1)
			var sum uint64
			for time.Now().Before(deadline) {
				for i := 0; i < 1<<18; i++ {
					x ^= x << 13
					x ^= x >> 7
					x ^= x << 17
					sum += x
				}
			}
			if sum == 42 {
				println("unlikely") // keep sum alive
			}
		}(g)
	}
	wg.Wait()
	distinct := map[int64]bool{}
	for _, id := range mids {
		distinct[id] = true
	}
	println("liveness: CPU goroutines ran on", len(distinct), "distinct worker Ms; busy window ms =", busyMS)
}

func main() {
	if s := os.Getenv("LIVENESS_BUSY_MS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			busyMS = n
		}
	}
	println("liveness: gomaxprocs =", runtime.GOMAXPROCS(0))

	// Arm a pure-JS timer on the main thread. It must fire on schedule
	// while the workers are busy; the callback is plain JavaScript, so
	// it needs nothing from the Go scheduler.
	js.Global().Call("eval",
		`globalThis.__livenessT0 = Date.now();
		 globalThis.__livenessJSFired = -1;
		 setTimeout(() => {
			globalThis.__livenessJSFired = Date.now() - globalThis.__livenessT0;
			console.log("liveness: JS setTimeout fired after", globalThis.__livenessJSFired, "ms (target `+strconv.Itoa(jsTimerMS)+`)");
		 }, `+strconv.Itoa(jsTimerMS)+`);`)

	start := time.Now()
	runOnNewM(worker) // main M parks in the event loop while the workers compute
	elapsed := int64(time.Since(start) / time.Millisecond)
	println("liveness: workers done after", elapsed, "ms")

	jsFired := js.Global().Get("__livenessJSFired").Int()
	goDelay := goTimerDelayMS.Load()
	println("liveness: JS setTimeout delay ms =", jsFired, "(target", jsTimerMS, ")")
	println("liveness: Go timer delay ms =", goDelay, "(target", goTimerMS, ")")

	bad := false
	if jsFired < jsTimerMS-10 || jsFired > jsTimerMS+300 {
		println("liveness: FAIL: JS setTimeout did not fire on schedule while workers were busy")
		bad = true
	}
	if goDelay < goTimerMS-10 || goDelay > goTimerMS+300 {
		println("liveness: FAIL: Go timer did not fire on schedule while workers were busy")
		bad = true
	}
	if elapsed < int64(busyMS) {
		println("liveness: FAIL: workers finished before the busy window; timers were not concurrent")
		bad = true
	}
	if bad {
		println("LIVENESS: FAIL")
		os.Exit(1)
	}
	println("LIVENESS: PASS")
}
