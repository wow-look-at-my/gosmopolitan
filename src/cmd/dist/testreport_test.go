// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

// runReport feeds lines through a testReport and returns what it wrote, plus
// the timings and the packages it counted as finished.
func runReport(inp string) (string, *testTimings, []string) {
	var out strings.Builder
	var timings testTimings
	var done []string
	rep := newTestReport(&out, &timings, func(pkg string) { done = append(done, pkg) })
	rep.Write([]byte(inp))
	rep.Flush()
	return out.String(), &timings, done
}

func TestReportDropsPassingOutput(t *testing.T) {
	const inp = `{"Action":"run","Package":"pkg","Test":"TestOne"}
{"Action":"output","Package":"pkg","Test":"TestOne","Output":"=== RUN   TestOne\n"}
{"Action":"output","Package":"pkg","Test":"TestOne","Output":"--- PASS: TestOne (0.25s)\n"}
{"Action":"pass","Package":"pkg","Test":"TestOne","Elapsed":0.25}
{"Action":"output","Package":"pkg","Output":"PASS\n"}
{"Action":"output","Package":"pkg","Output":"ok  \tpkg\t0.30s\n"}
{"Action":"pass","Package":"pkg","Elapsed":0.30}
`
	got, timings, done := runReport(inp)
	if len(done) != 1 || done[0] != "pkg" {
		t.Errorf("counted packages = %v, want [pkg]", done)
	}
	const want = "ok  \tpkg\t0.30s\n"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if strings.Contains(got, "=== RUN") {
		t.Errorf("a passing test's RUN line reached the log: %q", got)
	}
	if len(timings.all) != 1 {
		t.Fatalf("recorded %d timings, want 1", len(timings.all))
	}
	if timings.all[0].test != "TestOne" || timings.all[0].seconds != 0.25 {
		t.Errorf("timing = %+v, want TestOne at 0.25s", timings.all[0])
	}
}

func TestReportKeepsFailingOutput(t *testing.T) {
	const inp = `{"Action":"output","Package":"pkg","Test":"TestBad","Output":"=== RUN   TestBad\n"}
{"Action":"output","Package":"pkg","Test":"TestBad","Output":"    bad_test.go:9: got 3, want 4\n"}
{"Action":"output","Package":"pkg","Test":"TestBad","Output":"--- FAIL: TestBad (0.10s)\n"}
{"Action":"fail","Package":"pkg","Test":"TestBad","Elapsed":0.10}
{"Action":"output","Package":"pkg","Output":"FAIL\n"}
{"Action":"output","Package":"pkg","Output":"FAIL\tpkg\t0.12s\n"}
{"Action":"fail","Package":"pkg","Elapsed":0.12}
`
	got, _, _ := runReport(inp)
	for _, want := range []string{
		"=== RUN   TestBad\n",
		"    bad_test.go:9: got 3, want 4\n",
		"--- FAIL: TestBad (0.10s)\n",
		"FAIL\tpkg\t0.12s\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q; got %q", want, got)
		}
	}
}

// A failing test keeps the output of the passing tests that ran before it out
// of the log, so the reason for the failure is not buried.
func TestReportFailureIsNotBuried(t *testing.T) {
	const inp = `{"Action":"output","Package":"pkg","Test":"TestGood","Output":"=== RUN   TestGood\n"}
{"Action":"pass","Package":"pkg","Test":"TestGood","Elapsed":0.01}
{"Action":"output","Package":"pkg","Test":"TestBad","Output":"    bad_test.go:9: boom\n"}
{"Action":"fail","Package":"pkg","Test":"TestBad","Elapsed":0.02}
`
	got, _, _ := runReport(inp)
	if strings.Contains(got, "TestGood") {
		t.Errorf("a passing test reached the log: %q", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("the failure is missing: %q", got)
	}
}

// A binary that panics reports no result for the running test. Its output is
// the reason for the panic, so Flush must write it.
func TestReportFlushesUnfinished(t *testing.T) {
	const inp = `{"Action":"output","Package":"pkg","Test":"TestPanic","Output":"panic: runtime error\n"}
{"Action":"output","Package":"pkg","Test":"TestPanic","Output":"goroutine 1 [running]:\n"}
`
	got, _, _ := runReport(inp)
	for _, want := range []string{"panic: runtime error\n", "goroutine 1 [running]:\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q; got %q", want, got)
		}
	}
}

func TestReportPassesNonEventThrough(t *testing.T) {
	const inp = `# pkg
./bad.go:3:2: undefined: nope
{"Action":"fail","Package":"pkg","Elapsed":0.01}
`
	got, _, _ := runReport(inp)
	for _, want := range []string{"# pkg\n", "./bad.go:3:2: undefined: nope\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("a build error is missing %q; got %q", want, got)
		}
	}
}

// The writer receives arbitrary chunks, so a line can span two calls.
func TestReportSplitWrites(t *testing.T) {
	const inp = `{"Action":"output","Package":"pkg","Test":"TestBad","Output":"boom\n"}
{"Action":"fail","Package":"pkg","Test":"TestBad","Elapsed":0.02}
`
	var out strings.Builder
	var timings testTimings
	rep := newTestReport(&out, &timings, nil)
	for pos := 0; pos < len(inp); pos += 7 {
		end := min(pos+7, len(inp))
		rep.Write([]byte(inp[pos:end]))
	}
	rep.Flush()
	if !strings.Contains(out.String(), "boom") {
		t.Errorf("the failure is missing across split writes: %q", out.String())
	}
}

func TestTimingsReportNamesSlowest(t *testing.T) {
	var timings testTimings
	timings.add("pkg", "TestFast", 0.5)
	timings.add("pkg", "TestSlow", 90.0)
	timings.add("pkg", "TestMid", 4.0)

	var out strings.Builder
	timings.report(&out, 2)
	got := out.String()
	if !strings.Contains(got, "pkg.TestSlow") {
		t.Errorf("the slowest test is missing: %q", got)
	}
	if strings.Contains(got, "TestFast") {
		t.Errorf("the report is not limited to the slowest: %q", got)
	}
	slow := strings.Index(got, "TestSlow")
	mid := strings.Index(got, "TestMid")
	if slow < 0 || mid < 0 || slow > mid {
		t.Errorf("the report is not ordered by duration: %q", got)
	}
}
