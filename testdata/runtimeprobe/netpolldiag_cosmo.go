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
func cosmoNetpollDiag() (cycles, done uint64, enterNs, exitNs, nowNs int64, lastN, lastE int32, wakeEnter, wakeDone, acquired uint64)

// printNetpollDiag prints one sample of the darwin poller counters. The
// watchdog prints two samples a spin apart so a wedged run's log shows
// where a stall sits: cycles static with exit older than enter means
// stuck inside kevent, done < cycles means stuck between kevent and
// cycle end, and the sema counters (semawakeups entered/completed,
// sleep wakeups consumed) tell the M-parking side's story.
func printNetpollDiag(tag string) {
	cycles, done, enterNs, exitNs, nowNs, lastN, lastE, wakeEnter, wakeDone, acquired := cosmoNetpollDiag()
	fmt.Printf("diag %s: pollcycles=%d/%d sinceenter=%dms sinceexit=%dms lastn=%d laste=%d semawake=%d/%d acq=%d\n",
		tag, cycles, done, (nowNs-enterNs)/1e6, (nowNs-exitNs)/1e6, lastN, lastE,
		wakeEnter, wakeDone, acquired)
}
