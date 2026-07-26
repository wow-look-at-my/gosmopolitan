// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"internal/testenv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loopInlineSrc exercises the three parts of loop-aware inlining (see
// cmd/compile/internal/inline/loop.go). Every function in it is far too
// expensive for the flat 80-node inlining budget; what differs is where
// the cost sits.
const loopInlineSrc = `
package p

// mixLoop's cost (109) is almost entirely inside a loop. The loop cost
// discount would bring it to 59 - but that discount is off by default
// (it measured as a net loss; see loop.go), so mixLoop inlines only
// under -d=loopinlinediv=2.
func mixLoop(xs []uint32, k uint32) uint32 {
	h := k
	for i, x := range xs {
		h ^= x * 2654435761
		h = h<<13 | h>>19
		h += uint32(i) * 2246822519
		h ^= h >> 15
		h *= 3266489917
		h += x ^ k
		h = h<<7 | h>>25
		h -= x >> 3
		h ^= x * 2654435761
		h = h<<13 | h>>19
		h += uint32(i) * 2246822519
		h ^= h >> 15
		h *= 3266489917
		h += x ^ k
		h = h<<7 | h>>25
		h -= x >> 3
	}
	return h
}

// mixFlat is exactly mixLoop's arithmetic with the loop peeled away, at
// cost 99, and never inlines. It is what keeps the discount honest: were
// the budget simply raised, mixFlat would inline too.
func mixFlat(x, i, k uint32) uint32 {
	h := k
	h ^= x * 2654435761
	h = h<<13 | h>>19
	h += i * 2246822519
	h ^= h >> 15
	h *= 3266489917
	h += x ^ k
	h = h<<7 | h>>25
	h -= x >> 3
	h ^= x * 2654435761
	h = h<<13 | h>>19
	h += i * 2246822519
	h ^= h >> 15
	h *= 3266489917
	h += x ^ k
	h = h<<7 | h>>25
	h -= x >> 3
	return h
}

// bigLoop costs 153: over the flat 80-node budget, so the unmodified
// compiler inlines it nowhere, and under the 160 a call site one loop
// deep will pay. It is the default mechanism's demonstration - the same
// callee, accepted at the hot call site and refused at the cold one.
func bigLoop(xs []uint32, k uint32) uint32 {
	h := k
	for i, x := range xs {
		h ^= x * 2654435761
		h = h<<13 | h>>19
		h += uint32(i) * 2246822519
		h ^= h >> 15
		h *= 3266489917
		h += x ^ k
		h = h<<7 | h>>25
		h -= x >> 3
		h ^= x * 2654435761
		h = h<<13 | h>>19
		h += uint32(i) * 2246822519
		h ^= h >> 15
		h *= 3266489917
		h += x ^ k
		h = h<<7 | h>>25
		h -= x >> 3
		h ^= x*k + uint32(i)
		h = h<<11 | h>>21
		h += x*3 - uint32(i)
		h ^= h >> 9
		h *= 2654435761
		h -= x << 2
		h ^= uint32(i) * 3266489917
	}
	return h
}

var Sink uint32

// CallCold's calls each run once, so it must not get bigLoop.
func CallCold(xs []uint32) {
	Sink = mixLoop(xs, 1)
	Sink = mixFlat(2, 3, 4)
	Sink = bigLoop(xs, 5)
}

// CallHot's call runs once per element, so it can afford bigLoop.
func CallHot(xss [][]uint32) {
	for _, xs := range xss {
		Sink += bigLoop(xs, 6)
	}
}
`

// buildLoopInlineTest compiles loopInlineSrc with the given -gcflags and
// returns the compiler's -m diagnostics.
func buildLoopInlineTest(t *testing.T, gcflag string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/loopinline\ngo 1.26\n"), 0644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte(loopInlineSrc), 0644); err != nil {
		t.Fatalf("writing p.go: %v", err)
	}

	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-gcflags="+gcflag, ".")
	cmd.Dir = dir
	cmd = testenv.CleanCmdEnv(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v, output:\n%s", err, out)
	}
	return string(out)
}

func TestLoopInlining(t *testing.T) {
	testenv.MustHaveGoRun(t)
	t.Parallel()

	// The shipped default: loop nesting is acted on at the call site.
	on := buildLoopInlineTest(t, "-m=2")

	for _, tc := range []struct {
		diag string
		want bool
		why  string
	}{
		{"can inline bigLoop", true,
			"a function this package calls from inside a loop is analyzed with the larger budget"},
		{"inlining call to bigLoop", true,
			"the call site inside CallHot's loop should accept a callee an ordinary site will not"},
		{"can inline mixLoop", false,
			"the callee loop-cost discount is off by default; it measured as a net loss"},
		{"can inline mixFlat", false,
			"mixFlat is over the flat budget and has no loop to discount"},
	} {
		if got := strings.Contains(on, tc.diag); got != tc.want {
			t.Errorf("defaults: %q found = %v, want %v (%s)\ncompiler output:\n%s",
				tc.diag, got, tc.want, tc.why, on)
		}
	}

	// bigLoop is affordable in CallHot's loop but not on CallCold's
	// straight-line path, so exactly one of its two call sites inlines.
	// This is the whole point: the same callee, judged by where it is
	// called from rather than by what it costs alone.
	if n := strings.Count(on, "inlining call to bigLoop"); n != 1 {
		t.Errorf("%d call sites inlined bigLoop, want 1 (the one inside a loop)\n%s", n, on)
	}
}

// TestLoopInliningDiscount covers the callee-side cost discount, which is
// implemented and tested but off by default: measured on its own it cost
// +1.1% median across nine whole-task workloads (see loop.go).
func TestLoopInliningDiscount(t *testing.T) {
	testenv.MustHaveGoRun(t)
	t.Parallel()

	out := buildLoopInlineTest(t, "-m=2 -d=loopinlinediv=2")
	if !strings.Contains(out, "can inline mixLoop") {
		t.Errorf("with the discount on, mixLoop (cost 109, nearly all of it loop-nested) should inline:\n%s", out)
	}
	// Same arithmetic, no loop: the discount must not turn into a
	// general budget increase.
	if strings.Contains(out, "can inline mixFlat") {
		t.Errorf("with the discount on, mixFlat (cost 99, no loop) must still be too complex:\n%s", out)
	}
}

// TestLoopInliningDisabled checks that -d=loopinline=0 restores the
// previous inlining decisions exactly, so the mechanism can be turned off
// to bisect a regression.
func TestLoopInliningDisabled(t *testing.T) {
	testenv.MustHaveGoRun(t)
	t.Parallel()

	off := buildLoopInlineTest(t, "-m=2 -d=loopinline=0")
	for _, unwanted := range []string{
		"can inline mixLoop", "can inline mixFlat", "can inline bigLoop",
		"inlining call to mixLoop", "inlining call to bigLoop",
	} {
		if strings.Contains(off, unwanted) {
			t.Errorf("with loopinline=0, unexpectedly found %q:\n%s", unwanted, off)
		}
	}
	if !strings.Contains(off, "cannot inline bigLoop: function too complex") {
		t.Errorf("with loopinline=0, bigLoop should be reported too complex:\n%s", off)
	}
	// Even the discount flag must not revive it.
	both := buildLoopInlineTest(t, "-m=2 -d=loopinline=0,loopinlinediv=2")
	if strings.Contains(both, "can inline mixLoop") {
		t.Errorf("loopinline=0 must override the per-mechanism flags:\n%s", both)
	}
}

// TestLoopInliningTunables checks that each knob is wired up: turning any
// one of them down individually must lose exactly the inline it is
// responsible for.
func TestLoopInliningTunables(t *testing.T) {
	testenv.MustHaveGoRun(t)
	t.Parallel()

	for _, tc := range []struct {
		flags   string
		diag    string
		want    bool
		comment string
	}{
		{"", "inlining call to bigLoop", true, "defaults"},
		{" -d=loopinlinefactor=1", "inlining call to bigLoop", false,
			"call site budget does not grow with loop depth"},
		{" -d=loopinlinegrowth=1", "inlining call to bigLoop", false,
			"caller growth allowance exhausted"},
		{" -d=loopinlinebudget=80", "inlining call to bigLoop", false,
			"callee never gets an inline body"},
		{" -d=loopinlinediv=2", "can inline mixLoop", true, "discount enabled"},
		{" -d=loopinlinediv=2,loopinlinecredit=1", "can inline mixLoop", false,
			"discount capped to nothing"},
	} {
		out := buildLoopInlineTest(t, "-m=2"+tc.flags)
		if got := strings.Contains(out, tc.diag); got != tc.want {
			t.Errorf("gcflags %q (%s): %q found = %v, want %v\n%s",
				"-m=2"+tc.flags, tc.comment, tc.diag, got, tc.want, out)
		}
	}
}
