// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"io"
	"strings"
	"testing"
)

func TestProgressLineOrdersSlowestFirst(t *testing.T) {
	recent := []testTiming{
		{"pkg", "slowish", 0.3},
		{"pkg", "faster", 0.2},
		{"pkg", "fastest", 0.1},
	}
	got := progressLine(progressCounts{testsEnded: 30, testsStarted: 41, stepsDone: 4, steps: 8}, recent, 120)
	want := "[30 done/41 started, 4/8 steps 50%] 0.3s slowish, 0.2s faster, 0.1s fastest"
	if got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestProgressLineSingleSlowTest(t *testing.T) {
	recent := []testTiming{{"pkg", "reallyslowtest", 3.4}}
	got := progressLine(progressCounts{testsEnded: 1, testsStarted: 1, stepsDone: 1, steps: 10}, recent, 120)
	const want = "[1 done/1 started, 1/10 steps 10%] 3.4s reallyslowtest"
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
	timings.start()
	timings.start()
	timings.start()
	timings.add("pkg", "faster", 0.2)
	timings.add("pkg", "slowish", 0.3)
	var out strings.Builder
	pro := newTestProgress(&out, &timings, 8)
	pro.markDone("one")
	pro.markDone("two")
	pro.markDone("three")
	pro.markDone("four")
	pro.emit()
	const want = "[2 done/3 started, 4/8 steps 50%] 0.3s slowish, 0.2s faster\n"
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
	const width = 60
	got := progressLine(progressCounts{testsEnded: 3, testsStarted: 3, stepsDone: 1, steps: 2}, recent, width)
	if len(got) != width {
		t.Errorf("line is %d wide, want %d: %q", len(got), width, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("a truncated line must end in an ellipsis: %q", got)
	}
	if !strings.HasPrefix(got, "[3 done/3 started, 1/2 steps 50%] 0.3s aaaa") {
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

func TestProgressCountsEachStepOnce(t *testing.T) {
	var timings testTimings
	pro := newTestProgress(nil, &timings, 3)
	pro.markDone("one")
	pro.markDone("one")
	pro.markDone("two")
	if cnt := pro.counts(); cnt.stepsDone != 2 || cnt.steps != 3 {
		t.Errorf("steps = %d/%d, want 2/3", cnt.stepsDone, cnt.steps)
	}
}

// The step total counts dist tests. A variant run reports packages that are
// no dist test, and those must not count, or done overtakes total.
func TestPackageResultCountsOnlyItsOwnTest(t *testing.T) {
	var timings testTimings
	tst := &tester{testNames: map[string]bool{"bufio": true, "crypto/...:gofips140": true}}
	tst.progress = newTestProgress(nil, &timings, 2)
	tst.markPkgDone("bufio")
	tst.markPkgDone("bufio")
	tst.markPkgDone("crypto/internal/fips140/v1.26.0/aes")
	tst.markPkgDone("crypto/aes")
	if cnt := tst.progress.counts(); cnt.stepsDone != 1 || cnt.steps != 2 {
		t.Fatalf("after package results, steps = %d/%d, want 1/2", cnt.stepsDone, cnt.steps)
	}
	tst.markTestDone("crypto/...:gofips140")
	if cnt := tst.progress.counts(); cnt.stepsDone != 2 || cnt.steps != 2 {
		t.Errorf("after the variant ended, steps = %d/%d, want 2/2", cnt.stepsDone, cnt.steps)
	}
}

// Every test that ends moves the counter by one, subtests included, while the
// step holding them is still running. The total is the tests started so far,
// so a test that began and has not ended keeps done below it.
func TestProgressCountsEveryEndedTest(t *testing.T) {
	t.Setenv("COLUMNS", "200")
	tst := &tester{testNames: map[string]bool{"pkg": true}}
	var out strings.Builder
	tst.progress = newTestProgress(&out, &tst.timings, 4)
	rep := newTestReport(io.Discard, &tst.timings, tst.markPkgDone)

	rep.Write([]byte(`{"Action":"run","Package":"pkg","Test":"TestParent"}
{"Action":"run","Package":"pkg","Test":"TestParent/first"}
{"Action":"pass","Package":"pkg","Test":"TestParent/first","Elapsed":0.5}
`))
	tst.progress.emit()
	rep.Write([]byte(`{"Action":"run","Package":"pkg","Test":"TestParent/second"}
{"Action":"skip","Package":"pkg","Test":"TestParent/second","Elapsed":0.2}
`))
	tst.progress.emit()
	rep.Write([]byte(`{"Action":"pass","Package":"pkg","Test":"TestParent","Elapsed":0.8}
{"Action":"run","Package":"pkg","Test":"TestFails"}
{"Action":"fail","Package":"pkg","Test":"TestFails","Elapsed":1.5}
{"Action":"run","Package":"pkg","Test":"TestStillRunning"}
`))
	tst.progress.emit()
	rep.Write([]byte(`{"Action":"fail","Package":"pkg","Elapsed":3}
`))
	rep.Flush()

	const want = "[1 done/2 started, 0/4 steps 0%] 0.5s TestParent/first\n" +
		"[2 done/3 started, 0/4 steps 0%] 0.2s TestParent/second\n" +
		"[4 done/5 started, 0/4 steps 0%] 1.5s TestFails, 0.8s TestParent\n"
	if out.String() != want {
		t.Errorf("lines =\n%s\nwant\n%s", out.String(), want)
	}
	cnt := tst.progress.counts()
	if cnt.testsEnded != 4 || cnt.testsStarted != 5 || cnt.stepsDone != 1 {
		t.Errorf("after the package ended, counts = %+v, want 4 ended, 5 started, 1 step", cnt)
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
