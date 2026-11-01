// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// inotifyPreemptRun is how long the workers open and close watches.
const inotifyPreemptRun = 3 * time.Second

// inotifyUnderPreemption opens and closes watches while collections stop the
// world. A stop sends preemption signals, and on macOS a watch is host library
// code that holds a thread for milliseconds. The signals land inside it. The
// host library owns the g register there. A handler that trusts it faults on
// its own fault forever, and the stop never ends.
func inotifyUnderPreemption() error {
	dir, err := os.MkdirTemp("", "rp-inotify-preempt")
	if err != nil {
		return fmt.Errorf("mkdtemp: %w", err)
	}
	defer os.RemoveAll(dir)

	var stop atomic.Bool
	var failure atomic.Pointer[error]
	var group sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		group.Go(func() {
			for !stop.Load() {
				if err := inotifyOpenClose(dir); err != nil {
					failure.CompareAndSwap(nil, &err)
					return
				}
			}
		})
	}
	for deadline := time.Now().Add(inotifyPreemptRun); time.Now().Before(deadline); {
		runtime.GC()
	}
	stop.Store(true)
	group.Wait()
	if err := failure.Load(); err != nil {
		return *err
	}
	return nil
}

// inotifyOpenClose starts one watch and stops it.
func inotifyOpenClose(dir string) error {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		return fmt.Errorf("inotify_init1: %w", err)
	}
	defer syscall.Close(fd)
	if _, err := syscall.InotifyAddWatch(fd, dir, syscall.IN_CREATE); err != nil {
		return fmt.Errorf("inotify_add_watch: %w", err)
	}
	return nil
}
