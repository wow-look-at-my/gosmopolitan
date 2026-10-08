// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

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

// fallbackWidth is the line width to use when the terminal does not report one.
const fallbackWidth = 120

// progressPeriod is how often a progress line appears.
const progressPeriod = time.Second

// testProgress writes one line each second in which a test finished.
type testProgress struct {
	dst     io.Writer
	timings *testTimings

	lock  sync.Mutex
	steps int
	done  map[string]bool

	stop chan struct{}
	wait sync.WaitGroup
}

// progressCounts is what a progress line reports.
type progressCounts struct {
	testsEnded   int
	testsStarted int
	stepsDone    int
	steps        int
}

func newTestProgress(dst io.Writer, timings *testTimings, steps int) *testProgress {
	return &testProgress{
		dst:     dst,
		timings: timings,
		steps:   steps,
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

// markDone records that a named dist test finished.
func (pro *testProgress) markDone(name string) {
	pro.lock.Lock()
	defer pro.lock.Unlock()
	pro.done[name] = true
}

func (pro *testProgress) counts() progressCounts {
	ended, started := pro.timings.testCounts()
	pro.lock.Lock()
	defer pro.lock.Unlock()
	return progressCounts{
		testsEnded:   ended,
		testsStarted: started,
		stepsDone:    len(pro.done),
		steps:        pro.steps,
	}
}

func (pro *testProgress) emit() {
	// The drain is what says whether this second finished anything, and it empties the buffer on the way out.
	recent := pro.timings.drainRecent()
	if len(recent) == 0 {
		return
	}
	fmt.Fprintln(pro.dst, progressLine(pro.counts(), recent, lineWidth()))
}

// progressLine builds the line. It is separate from emit so a test can check
// the text without a clock.
//
// Each test that ended counts one, subtests included, over the tests started
// so far: a test is known only once its binary runs it, so that total grows.
// The steps that follow are fixed at the start of the run and carry the
// percentage, since they are what says how much is left to announce.
func progressLine(cnt progressCounts, recent []testTiming, width int) string {
	pct := 0
	if cnt.steps > 0 {
		pct = cnt.stepsDone * 100 / cnt.steps
	}
	prefix := fmt.Sprintf("[%d done/%d started, %d/%d steps %d%%] ",
		cnt.testsEnded, cnt.testsStarted, cnt.stepsDone, cnt.steps, pct)

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
