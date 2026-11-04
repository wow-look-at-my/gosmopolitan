// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"runtime/debug"
	"time"
)

func init() {
	register("PrintMemoryLimit", PrintMemoryLimit)
	register("MemoryLimitChurn", MemoryLimitChurn)
}

func PrintMemoryLimit() {
	println(debug.SetMemoryLimit(-1))
}

var (
	churnLive [][]byte
	churnSink []byte
)

// MemoryLimitChurn keeps 120 MiB live and allocates garbage fast. Run it with a
// high GOGC, so that only the memory limit bounds the heap.
func MemoryLimitChurn() {
	for range 60 {
		buf := make([]byte, 2<<20)
		for idx := range buf {
			buf[idx] = 1
		}
		churnLive = append(churnLive, buf)
	}
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		buf := make([]byte, 4<<20)
		for idx := 0; idx < len(buf); idx += 4096 {
			buf[idx] = 1
		}
		churnSink = buf
	}
	println("OK")
}
