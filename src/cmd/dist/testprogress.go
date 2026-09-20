// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// fallbackWidth is the line width to use when the terminal does not report
// one. A log file and a CI runner both reach this.
const fallbackWidth = 120

// progressPeriod is how often a progress line appears.
const progressPeriod = time.Second

// testProgress writes one line each second. The line carries how far the run
// has come and which tests ended during that second, slowest first.
//
// This names a slow test while the run is still going, which is what a
// reader wanted from verbose output.
type testProgress struct {
	dst     io.Writer
	timings *testTimings

	lock  sync.Mutex
	total int
	done  map[string]bool

	stop chan struct{}
	wait sync.WaitGroup
}

func newTestProgress(dst io.Writer, timings *testTimings, total int) *testProgress {
	return &testProgress{
		dst:     dst,
		timings: timings,
		total:   total,
		done:    make(map[string]bool),
		stop:    make(chan struct{}),
	}
}

// start runs the ticker until finish is called.
func (pro *testProgress) start() {
	pro.wait.Add(1)
	go func() {
		defer pro.wait.Done()
		tick := time.NewTicker(progressPeriod)
		defer tick.Stop()
		for {
			select {
			case <-pro.stop:
				return
			case <-tick.C:
				pro.emit()
			}
		}
	}()
}

// finish stops the ticker and waits for the goroutine to return.
func (pro *testProgress) finish() {
	close(pro.stop)
	pro.wait.Wait()
}

// markDone records that a named dist test finished. A dist test can hold
// several commands, so the name repeats and the map keeps the count right.
func (pro *testProgress) markDone(name string) {
	pro.lock.Lock()
	defer pro.lock.Unlock()
	pro.done[name] = true
}

func (pro *testProgress) counts() (done, total int) {
	pro.lock.Lock()
	defer pro.lock.Unlock()
	return len(pro.done), pro.total
}

func (pro *testProgress) emit() {
	done, total := pro.counts()
	recent := pro.timings.drainRecent()
	fmt.Fprintln(pro.dst, progressLine(done, total, recent, lineWidth()))
}

// progressLine builds the line. It is separate from emit so a test can check
// the text without a clock.
func progressLine(done, total int, recent []testTiming, width int) string {
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	prefix := fmt.Sprintf("[%d/%d %d%%] ", done, total, pct)

	var parts []string
	for _, tng := range recent {
		parts = append(parts, fmt.Sprintf("%.1fs %s", tng.seconds, tng.test))
	}
	line := prefix + strings.Join(parts, ", ")
	if len(line) > width {
		if width <= 3 {
			return line[:width]
		}
		line = line[:width-3] + "..."
	}
	return line
}

// lineWidth reports the width to hold a progress line to. A terminal reports
// its width in COLUMNS. Nothing else does, so the fallback covers a log.
func lineWidth() int {
	if val := os.Getenv("COLUMNS"); val != "" {
		if num, err := strconv.Atoi(val); err == nil && num > 0 {
			return num
		}
	}
	return fallbackWidth
}
