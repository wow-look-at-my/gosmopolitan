// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"fmt"
	_ "unsafe" // for go:linkname
)

// cosmoNetpollDiag reads the darwin poller's forensic counters from the
// runtime (netpoll_cosmo_xnu.go). Zero everywhere on Linux hosts, where
// the epoll poller is used instead.
//
//go:linkname cosmoNetpollDiag runtime.cosmoNetpollDiag
func cosmoNetpollDiag() (cycles uint64, enterNs, exitNs, nowNs int64, lastN, lastE, pending int32)

// printNetpollDiag prints one sample of the darwin poller counters. The
// watchdog prints two samples a spin apart so a wedged run's log shows
// whether the poller is frozen inside poll(2) (cycles static, now-enter
// large, exit < enter), wedged elsewhere in its cycle (cycles static,
// exit > enter), or cycling fine while a mutator sleeps through wakeups
// (cycles advancing).
func printNetpollDiag(tag string) {
	cycles, enterNs, exitNs, nowNs, lastN, lastE, pending := cosmoNetpollDiag()
	fmt.Printf("diag %s: pollcycles=%d sinceenter=%dms sinceexit=%dms lastn=%d laste=%d pending=%d\n",
		tag, cycles, (nowNs-enterNs)/1e6, (nowNs-exitNs)/1e6, lastN, lastE, pending)
}
