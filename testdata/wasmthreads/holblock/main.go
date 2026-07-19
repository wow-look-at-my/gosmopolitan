// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// holblock is the GOWASM=threads head-of-line-blocking gate (B4): an
// asynchronous host event whose Go handler blocks forever must not
// prevent later host events from being serviced.
//
// The main goroutine registers two js.FuncOf callbacks and arms two JS
// setTimeouts: blocker (fires at 50ms, parks forever on a channel) and
// probe (fires at 300ms). Under GOWASM=threads every asynchronous host
// event gets its own event goroutine (wasmEventGoroutine), so probe must
// run on schedule even while blocker's goroutine is parked. probe then
// unblocks blocker so the program can exit cleanly.
//
//	GOOS=js GOARCH=wasm GOWASM=threads go build -ldflags=-checklinkname=0 -o holblock.wasm ./holblock
//	GOMAXPROCS=2 node $GOROOT/lib/wasm/wasm_exec_node.js holblock.wasm
package main

import (
	"fmt"
	"os"
	"syscall/js"
	"time"
)

func main() {
	start := time.Now()
	unblock := make(chan struct{})
	blockerEntered := make(chan struct{})
	blockerDone := make(chan struct{})
	probeFired := make(chan int64, 1)

	blocker := js.FuncOf(func(this js.Value, args []js.Value) any {
		close(blockerEntered)
		<-unblock // blocks this event handler goroutine "forever"
		close(blockerDone)
		return nil
	})
	defer blocker.Release()

	probe := js.FuncOf(func(this js.Value, args []js.Value) any {
		probeFired <- time.Since(start).Milliseconds()
		return nil
	})
	defer probe.Release()

	setTimeout := js.Global().Get("setTimeout")
	setTimeout.Invoke(blocker, 50)
	setTimeout.Invoke(probe, 300)

	select {
	case <-blockerEntered:
	case <-time.After(5 * time.Second):
		fmt.Println("HOLBLOCK: FAIL (blocker never entered)")
		os.Exit(1)
	}

	// The blocker's event goroutine is now parked forever. The probe
	// event must still be serviced.
	select {
	case ms := <-probeFired:
		fmt.Println("holblock: probe fired at ms =", ms, "(target 300 )")
		if ms < 250 || ms > 2000 {
			fmt.Println("HOLBLOCK: FAIL (probe delay out of range)")
			os.Exit(1)
		}
	case <-time.After(10 * time.Second):
		fmt.Println("HOLBLOCK: FAIL (probe event head-of-line blocked)")
		os.Exit(1)
	}

	// Unblock the first handler; its goroutine must finish normally.
	close(unblock)
	select {
	case <-blockerDone:
	case <-time.After(5 * time.Second):
		fmt.Println("HOLBLOCK: FAIL (blocker never finished after unblock)")
		os.Exit(1)
	}

	// Part 2: a SYNCHRONOUS nested callback (JavaScript invoking a Go
	// callback from a JavaScript call Go makes) - the non-blocking case
	// must round-trip its result. A nested callback that BLOCKS is
	// documented-unsupported: the synchronous reentry borrows the caller
	// goroutine and the main M (strict LIFO on the live JS sandwich), so
	// it head-of-line-blocks later events until it returns - see
	// WASM_SHORTCOMINGS.md.
	nested := js.FuncOf(func(this js.Value, args []js.Value) any {
		return args[0].Int() + 7
	})
	defer nested.Release()
	callSync := js.Global().Call("eval", "(f, x) => f(x)")
	if got := callSync.Invoke(nested, 35).Int(); got != 42 {
		fmt.Println("HOLBLOCK: FAIL (nested sync callback result:", got, ")")
		os.Exit(1)
	}
	fmt.Println("holblock: nested sync callback returned 42")

	fmt.Println("HOLBLOCK: PASS")
}
