// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// CPU profiling test for js/wasm. The general CPU profiling tests live in
// pprof_test.go, which does not build on js; this file gives the js port
// end-to-end coverage of the wasm loop-backedge CPU sampler: profile
// collection, sample volume, and per-function attribution.

package pprof

import (
	"bytes"
	"internal/profile"
	"strings"
	"testing"
	"time"
)

//go:noinline
func cpuHogJS1(x uint64, n int) uint64 {
	for i := 0; i < n; i++ {
		x = x*2654435761 + 1
	}
	return x
}

//go:noinline
func cpuHogJS2(x uint64, n int) uint64 {
	for i := 0; i < n; i++ {
		x = x*2654435761 + 3
	}
	return x
}

var cpuHogJSSink uint64

func TestCPUProfileJS(t *testing.T) {
	// Run two hot functions with identical loop bodies in a 70:30
	// iteration ratio and check that the profile sees roughly that
	// split. Retry with a longer duration once in case the host is
	// slow or loaded.
	duration := 1 * time.Second
	for {
		hog1, hog2, total := cpuProfileJSCounts(t, duration)
		t.Logf("duration %v: %d samples: cpuHogJS1=%d cpuHogJS2=%d", duration, total, hog1, hog2)

		// Mirror profileOk in pprof_test.go: accept 10 or more
		// samples as evidence that profiling occurs at all.
		if total >= 10 && hog1 > 0 && hog2 > 0 && hog1 > hog2 {
			return
		}
		duration *= 2
		if duration > 8*time.Second {
			t.Fatalf("not enough samples or implausible split: %d samples, cpuHogJS1=%d, cpuHogJS2=%d", total, hog1, hog2)
		}
		t.Logf("retrying with %v duration", duration)
	}
}

// cpuProfileJSCounts profiles the 70:30 workload for roughly dur and
// returns the sample counts attributed to each hog and in total.
func cpuProfileJSCounts(t *testing.T, dur time.Duration) (hog1, hog2, total int64) {
	var buf bytes.Buffer
	if err := StartCPUProfile(&buf); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		cpuHogJSSink = cpuHogJS1(cpuHogJSSink, 70000)
		cpuHogJSSink = cpuHogJS2(cpuHogJSSink, 30000)
	}
	StopCPUProfile()

	p, err := profile.Parse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CheckValid(); err != nil {
		t.Fatal(err)
	}
	for _, sample := range p.Sample {
		count := sample.Value[0]
		total += count
		for _, loc := range sample.Location {
			for _, line := range loc.Line {
				switch {
				case strings.Contains(line.Function.Name, "cpuHogJS1"):
					hog1 += count
				case strings.Contains(line.Function.Name, "cpuHogJS2"):
					hog2 += count
				}
			}
		}
	}
	return hog1, hog2, total
}
