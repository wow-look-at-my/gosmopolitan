// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// testReport turns test2json events into the output a reader acts on, and
// records how long each test took.
//
// The test binary runs verbosely, because a per-test duration exists only in
// verbose output. This reads those events and writes the failures, so a log
// carries no line for a test that passed. One package reaches about 9900
// subtests, and each one costs a line under plain -v.
//
// A buffer holds each test's output until that test reports a result. A pass
// or a skip drops the buffer and keeps the duration. A failure writes the
// buffer through unchanged.
type testReport struct {
	dst     io.Writer
	timings *testTimings

	// pkgDone reports a package that reached a result. One command can carry
	// several packages, so this is what counts a run's progress.
	pkgDone func(pkg string)

	lineBuf bytes.Buffer // holds an incomplete line between Write calls

	// bufs holds each scope's pending output, keyed by package and test. The
	// empty test name is the package's own scope. order keeps the keys in the
	// order they first appeared, so a crash flushes them as they arrived.
	bufs  map[string]*bytes.Buffer
	order []string
}

// testTiming records one test's duration.
type testTiming struct {
	pkg     string
	test    string
	seconds float64
}

// testTimings collects durations from every test command. The commands run
// in parallel, so the lock is required.
type testTimings struct {
	lock sync.Mutex
	all  []testTiming

	// recent holds the tests that ended since the progress line last drained
	// the buffer.
	recent []testTiming
}

func (tim *testTimings) add(pkg, test string, seconds float64) {
	tim.lock.Lock()
	defer tim.lock.Unlock()
	tim.all = append(tim.all, testTiming{pkg, test, seconds})
	tim.recent = append(tim.recent, testTiming{pkg, test, seconds})
}

// drainRecent returns the tests that ended since the last call, slowest
// first, and empties the buffer.
func (tim *testTimings) drainRecent() []testTiming {
	tim.lock.Lock()
	defer tim.lock.Unlock()
	if len(tim.recent) == 0 {
		return nil
	}
	out := tim.recent
	tim.recent = nil
	sort.Slice(out, func(one, two int) bool {
		return out[one].seconds > out[two].seconds
	})
	return out
}

// report writes the slowest tests. This names the test that spent a slow
// package's time, which a per-package total cannot do.
func (tim *testTimings) report(dst io.Writer, most int) {
	tim.lock.Lock()
	defer tim.lock.Unlock()
	if len(tim.all) == 0 {
		return
	}
	// A test under the floor is not the reason a suite is slow, and a table
	// padded with them hides the ones that are.
	const floorSeconds = 0.01
	var all []testTiming
	for _, tng := range tim.all {
		if tng.seconds >= floorSeconds {
			all = append(all, tng)
		}
	}
	if len(all) == 0 {
		return
	}
	sort.Slice(all, func(one, two int) bool {
		return all[one].seconds > all[two].seconds
	})
	if len(all) > most {
		all = all[:most]
	}
	fmt.Fprintf(dst, "\nSlowest tests:\n")
	for _, tng := range all {
		fmt.Fprintf(dst, "%8.2fs  %s.%s\n", tng.seconds, tng.pkg, tng.test)
	}
}

// testEvent is the part of a test2json event this reader acts on.
type testEvent struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
	Output  string
}

func newTestReport(dst io.Writer, timings *testTimings, pkgDone func(pkg string)) *testReport {
	return &testReport{
		dst:     dst,
		timings: timings,
		pkgDone: pkgDone,
		bufs:    make(map[string]*bytes.Buffer),
	}
}

func (rep *testReport) Write(inp []byte) (int, error) {
	total := len(inp)
	for len(inp) > 0 {
		pos := bytes.IndexByte(inp, '\n')
		if pos < 0 {
			rep.lineBuf.Write(inp)
			break
		}
		var line []byte
		if rep.lineBuf.Len() > 0 {
			rep.lineBuf.Write(inp[:pos+1])
			line = rep.lineBuf.Bytes()
		} else {
			line = inp[:pos+1]
		}
		inp = inp[pos+1:]
		rep.process(line)
		rep.lineBuf.Reset()
	}
	return total, nil
}

// Flush writes every buffer that never reached a result, then the partial
// line. A test binary that panics reports no result for the test that was
// running, and that test's output is the reason for the panic.
func (rep *testReport) Flush() {
	for _, key := range rep.order {
		if buf := rep.bufs[key]; buf != nil && buf.Len() > 0 {
			rep.dst.Write(buf.Bytes())
		}
	}
	rep.bufs = make(map[string]*bytes.Buffer)
	rep.order = nil
	if rep.lineBuf.Len() > 0 {
		rep.dst.Write(rep.lineBuf.Bytes())
		rep.lineBuf.Reset()
	}
}

func (rep *testReport) bufFor(key string) *bytes.Buffer {
	buf := rep.bufs[key]
	if buf == nil {
		buf = new(bytes.Buffer)
		rep.bufs[key] = buf
		rep.order = append(rep.order, key)
	}
	return buf
}

// flushKey writes a scope's output and drops the buffer.
func (rep *testReport) flushKey(key string) {
	if buf := rep.bufs[key]; buf != nil {
		if buf.Len() > 0 {
			rep.dst.Write(buf.Bytes())
		}
		delete(rep.bufs, key)
	}
}

func (rep *testReport) process(line []byte) {
	if len(line) == 0 || line[0] != '{' {
		// Not an event. A build error arrives this way when the go command
		// writes it outside the JSON stream, and it must reach the reader.
		rep.dst.Write(line)
		return
	}
	var evt testEvent
	if err := json.Unmarshal(line, &evt); err != nil {
		rep.dst.Write(line)
		return
	}

	key := evt.Package + "\t" + evt.Test
	switch evt.Action {
	case "output":
		if evt.Test == "" && isBareResult(evt.Output) {
			// The lone PASS or FAIL line adds nothing to the package's own
			// result line, which carries the name and the duration.
			return
		}
		rep.bufFor(key).WriteString(evt.Output)
	case "pass", "skip":
		if evt.Test == "" {
			// The package scope holds its result line.
			rep.markPkgDone(evt.Package)
			rep.flushKey(key)
			return
		}
		rep.timings.add(evt.Package, evt.Test, evt.Elapsed)
		delete(rep.bufs, key)
	case "fail":
		if evt.Test != "" {
			rep.timings.add(evt.Package, evt.Test, evt.Elapsed)
		} else {
			rep.markPkgDone(evt.Package)
		}
		rep.flushKey(key)
	}
}

func (rep *testReport) markPkgDone(pkg string) {
	if rep.pkgDone != nil && pkg != "" {
		rep.pkgDone(pkg)
	}
}

// isBareResult reports whether a line is the test binary's own PASS or FAIL
// line, which carries no package name and no duration.
func isBareResult(out string) bool {
	switch strings.TrimRight(out, "\n") {
	case "PASS", "FAIL":
		return true
	}
	return false
}
