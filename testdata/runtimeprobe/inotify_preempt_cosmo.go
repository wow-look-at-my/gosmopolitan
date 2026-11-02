// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// inotifyPreemptRun is how long the workers add and remove watches.
const inotifyPreemptRun = 3 * time.Second

// inotifyUnderPreemption adds and removes watches while collections stop the
// world. A stop sends preemption signals, and on macOS a watch is host library
// code that holds a thread for milliseconds. The signals land inside it. The
// host library owns the g register there. A handler that trusts it faults on
// its own fault forever, and the stop never ends. Each worker keeps one
// instance, because Linux counts a closed instance against the user for a while.
func inotifyUnderPreemption() error {
	dir, err := os.MkdirTemp("", "rp-inotify-preempt")
	if err != nil {
		return fmt.Errorf("mkdtemp: %w", err)
	}
	defer os.RemoveAll(dir)

	var stop atomic.Bool
	var rounds atomic.Int64
	var failure atomic.Pointer[error]
	var group sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		group.Go(func() {
			fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
			if err != nil {
				err = fmt.Errorf("inotify_init1: %w", err)
				failure.CompareAndSwap(nil, &err)
				return
			}
			defer syscall.Close(fd)
			for !stop.Load() {
				if err := inotifyAddRemove(fd, dir); err != nil {
					failure.CompareAndSwap(nil, &err)
					return
				}
				rounds.Add(1)
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
	if rounds.Load() == 0 {
		return errors.New("no watch added and removed")
	}
	return nil
}

// inotifyAddRemove starts one watch and stops it.
func inotifyAddRemove(fd int, dir string) error {
	wd, err := syscall.InotifyAddWatch(fd, dir, syscall.IN_CREATE)
	if err != nil {
		return fmt.Errorf("inotify_add_watch: %w", err)
	}
	if _, err := syscall.InotifyRmWatch(fd, uint32(wd)); err != nil {
		return fmt.Errorf("inotify_rm_watch: %w", err)
	}
	return nil
}
