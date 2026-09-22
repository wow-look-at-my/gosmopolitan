// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

func TestProgressLineOrdersSlowestFirst(t *testing.T) {
	recent := []testTiming{
		{"pkg", "slowish", 0.3},
		{"pkg", "faster", 0.2},
		{"pkg", "fastest", 0.1},
	}
	got := progressLine(4, 8, recent, 120)
	if !strings.HasPrefix(got, "[4/8 50%] ") {
		t.Errorf("prefix missing from %q", got)
	}
	want := "[4/8 50%] 0.3s slowish, 0.2s faster, 0.1s fastest"
	if got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestProgressLineSingleSlowTest(t *testing.T) {
	recent := []testTiming{{"pkg", "reallyslowtest", 3.4}}
	got := progressLine(1, 10, recent, 120)
	const want = "[1/10 10%] 3.4s reallyslowtest"
	if got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// A tick that finished no test has no name to report, so it writes nothing.
// The counter moved, and a line carrying it alone is what this drops.
func TestProgressQuietTickWritesNothing(t *testing.T) {
	var timings testTimings
	var out strings.Builder
	pro := newTestProgress(&out, &timings, 10)
	pro.markDone("one")
	pro.markDone("two")
	pro.emit()
	if out.String() != "" {
		t.Errorf("a tick with no finished test wrote %q, want nothing", out.String())
	}
}

// A tick that finished tests writes one line, naming them slowest first after
// the counter.
func TestProgressBusyTickWritesLine(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	var timings testTimings
	timings.add("pkg", "faster", 0.2)
	timings.add("pkg", "slowish", 0.3)
	var out strings.Builder
	pro := newTestProgress(&out, &timings, 8)
	pro.markDone("one")
	pro.markDone("two")
	pro.markDone("three")
	pro.markDone("four")
	pro.emit()
	const want = "[4/8 50%] 0.3s slowish, 0.2s faster\n"
	if out.String() != want {
		t.Errorf("line = %q, want %q", out.String(), want)
	}
	// The drain emptied the buffer, so a tick with nothing new writes nothing.
	pro.emit()
	if out.String() != want {
		t.Errorf("a second tick wrote %q, want %q", out.String(), want)
	}
}

func TestProgressLineEllipsizes(t *testing.T) {
	recent := []testTiming{
		{"pkg", "aaaaaaaaaaaaaaaaaaaa", 0.3},
		{"pkg", "bbbbbbbbbbbbbbbbbbbb", 0.2},
		{"pkg", "cccccccccccccccccccc", 0.1},
	}
	const width = 40
	got := progressLine(1, 2, recent, width)
	if len(got) != width {
		t.Errorf("line is %d wide, want %d: %q", len(got), width, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("a truncated line must end in an ellipsis: %q", got)
	}
	if !strings.HasPrefix(got, "[1/2 50%] 0.3s aaaa") {
		t.Errorf("truncation dropped the start of the line: %q", got)
	}
}

// A run that has not learned its total writes nothing either, rather than a
// counter over zero.
func TestProgressZeroTotalWritesNothing(t *testing.T) {
	var timings testTimings
	var out strings.Builder
	pro := newTestProgress(&out, &timings, 0)
	pro.emit()
	if out.String() != "" {
		t.Errorf("a tick with no finished test wrote %q, want nothing", out.String())
	}
}

func TestProgressCountsEachTestOnce(t *testing.T) {
	var timings testTimings
	pro := newTestProgress(nil, &timings, 3)
	pro.markDone("one")
	pro.markDone("one")
	pro.markDone("two")
	done, total := pro.counts()
	if done != 2 || total != 3 {
		t.Errorf("counts = %d/%d, want 2/3", done, total)
	}
}

// A test too fast to matter is not the reason a suite is slow, so the table
// leaves it out rather than padding with it.
func TestTimingsReportSkipsTrivial(t *testing.T) {
	var timings testTimings
	timings.add("pkg", "TestReal", 0.50)
	timings.add("pkg", "TestTrivial", 0.001)

	var out strings.Builder
	timings.report(&out, 25)
	got := out.String()
	if !strings.Contains(got, "TestReal") {
		t.Errorf("a real duration is missing: %q", got)
	}
	if strings.Contains(got, "TestTrivial") {
		t.Errorf("a trivial duration padded the table: %q", got)
	}
}

// Nothing above the floor means no table at all, rather than an empty one.
func TestTimingsReportSilentWhenAllTrivial(t *testing.T) {
	var timings testTimings
	timings.add("pkg", "TestTrivial", 0.001)

	var out strings.Builder
	timings.report(&out, 25)
	if out.String() != "" {
		t.Errorf("report = %q, want nothing", out.String())
	}
}

func TestDrainRecentEmptiesAndSorts(t *testing.T) {
	var timings testTimings
	timings.add("pkg", "fast", 0.1)
	timings.add("pkg", "slow", 2.0)

	first := timings.drainRecent()
	if len(first) != 2 {
		t.Fatalf("drained %d, want 2", len(first))
	}
	if first[0].test != "slow" {
		t.Errorf("drain is not slowest first: %+v", first)
	}
	if second := timings.drainRecent(); second != nil {
		t.Errorf("a second drain returned %+v, want nothing", second)
	}
	// The full record keeps every test, so the final report is complete.
	if len(timings.all) != 2 {
		t.Errorf("the full record holds %d, want 2", len(timings.all))
	}
}
