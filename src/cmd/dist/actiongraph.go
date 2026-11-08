// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// slowActionCount is how many actions reportSlowActions names.
const slowActionCount = 15

// graphAction is the part of one go command action graph entry that
// reportSlowActions reads.
type graphAction struct {
	Mode      string
	Package   string
	TimeReady time.Time
	TimeStart time.Time
	TimeDone  time.Time
	Cmd       []string
}

// reportSlowActions names the actions of a step that took the most wall time,
// from the action graph that go test wrote to file. A step line says what a
// step cost. These lines say which compile, link or test run cost it.
func reportSlowActions(out io.Writer, step, file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(out, "dist action ERROR %s: no action graph: %v\n", step, err)
		return
	}
	var actions []graphAction
	if err := json.Unmarshal(data, &actions); err != nil {
		fmt.Fprintf(out, "dist action ERROR %s: action graph %s: %v\n", step, file, err)
		return
	}
	var timed []graphAction
	var first, last time.Time
	compiles, ran := 0, 0
	for _, act := range actions {
		if act.Mode == "build" {
			compiles++
			if len(act.Cmd) > 0 {
				ran++
			}
		}
		if !act.TimeStart.IsZero() && !act.TimeDone.IsZero() {
			timed = append(timed, act)
			if first.IsZero() || act.TimeReady.Before(first) {
				first = act.TimeReady
			}
			if act.TimeDone.After(last) {
				last = act.TimeDone
			}
		}
	}
	// A compile that ran a command missed the cache.
	fmt.Fprintf(out, "dist action %.3fs span %s %d of %d compiles ran\n", last.Sub(first).Seconds(), step, ran, compiles)
	sort.SliceStable(timed, func(left, right int) bool {
		return timed[left].TimeDone.Sub(timed[left].TimeStart) > timed[right].TimeDone.Sub(timed[right].TimeStart)
	})
	if len(timed) > slowActionCount {
		timed = timed[:slowActionCount]
	}
	for _, act := range timed {
		// The wait is the time the action was ready and had no worker.
		fmt.Fprintf(out, "dist action %.3fs wait %.3fs %s %s %s\n",
			act.TimeDone.Sub(act.TimeStart).Seconds(), act.TimeStart.Sub(act.TimeReady).Seconds(), step, act.Mode, act.Package)
	}
}
