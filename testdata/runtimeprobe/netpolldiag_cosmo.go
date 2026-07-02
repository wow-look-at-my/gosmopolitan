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
func cosmoNetpollDiag() (cycles, done uint64, enterNs, exitNs, nowNs int64, lastN, lastE, pending int32, mutEnter, mutSet, mutDone, wakeEnter, wakeDone, acquired uint64)

// printNetpollDiag prints one sample of the darwin poller counters. The
// watchdog prints two samples a spin apart so a wedged run's log shows
// where the wedge sits: cycles static with exit older than enter means
// stuck inside poll(2); done < cycles means stuck in the poller's cycle
// tail (drain/netpollready/unlock/semawakeup); done == cycles with
// mutSet < mutEnter means a mutator is asleep on a free xnuMtxset -
// then flat wake counters convict unlock2Wake's decision and advancing
// wakes with lagging acquired convict the parking primitive.
func printNetpollDiag(tag string) {
	cycles, done, enterNs, exitNs, nowNs, lastN, lastE, pending, mutEnter, mutSet, mutDone, wakeEnter, wakeDone, acquired := cosmoNetpollDiag()
	fmt.Printf("diag %s: pollcycles=%d/%d sinceenter=%dms sinceexit=%dms lastn=%d laste=%d pending=%d mut=%d/%d/%d semawake=%d/%d acq=%d\n",
		tag, cycles, done, (nowNs-enterNs)/1e6, (nowNs-exitNs)/1e6, lastN, lastE, pending,
		mutEnter, mutSet, mutDone, wakeEnter, wakeDone, acquired)
}
