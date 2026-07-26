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

// mixLoop's cost (109) is almost entirely inside a loop, which is charged
// at a discount, bringing it to 59: inlinable with loop-aware inlining,
// too complex without it.
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

// mixFlat is exactly mixLoop's arithmetic with the loop peeled away, and
// at cost 99 it stays too complex either way. It is what keeps this test
// honest: had the budget simply been raised, mixFlat would inline too.
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

// bigLoop costs 167, or 88 once discounted - inlinable in principle, but
// more than an ordinary call site will pay. Only a call site inside a loop
// accepts it.
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
		h = h<<17 | h>>15
		h += x | k
	}
	return h
}

var Sink uint32

// CallCold's calls all run once: it gets mixLoop but must not get bigLoop.
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

	on := buildLoopInlineTest(t, "-m=2")
	off := buildLoopInlineTest(t, "-m=2 -d=loopinline=0")

	has := func(out, substr string) bool { return strings.Contains(out, substr) }

	for _, tc := range []struct {
		what    string
		diag    string
		wantOn  bool
		wantOff bool
		whyOn   string
		whyOff  string
	}{
		{
			what:    "a loop-dominated function",
			diag:    "can inline mixLoop",
			wantOn:  true,
			wantOff: false,
			whyOn:   "loop-nested cost is charged at a discount, which should bring mixLoop under budget",
			whyOff:  "without the discount mixLoop is over the flat budget",
		},
		{
			what:    "the same arithmetic without a loop",
			diag:    "can inline mixFlat",
			wantOn:  false,
			wantOff: false,
			whyOn:   "the discount must apply to loop-nested code only, not raise the budget for everything",
			whyOff:  "mixFlat is over the flat budget",
		},
		{
			what:    "an expensive callee at a call site inside a loop",
			diag:    "inlining call to bigLoop",
			wantOn:  true,
			wantOff: false,
			whyOn:   "a call site nested in a loop should accept a more expensive callee",
			whyOff:  "without loop-aware inlining no call site accepts it",
		},
	} {
		for _, run := range []struct {
			name string
			out  string
			want bool
			why  string
		}{
			{"loopinline=1", on, tc.wantOn, tc.whyOn},
			{"loopinline=0", off, tc.wantOff, tc.whyOff},
		} {
			if got := has(run.out, tc.diag); got != run.want {
				t.Errorf("%s: %s: %q found = %v, want %v (%s)\ncompiler output:\n%s",
					run.name, tc.what, tc.diag, got, run.want, run.why, run.out)
			}
		}
	}

	// bigLoop is affordable in CallHot's loop but not on CallCold's
	// straight-line path, so exactly one of its two call sites inlines.
	if n := strings.Count(on, "inlining call to bigLoop"); n != 1 {
		t.Errorf("loopinline=1: %d call sites inlined bigLoop, want 1 (the one inside a loop)\n%s", n, on)
	}
}

// TestLoopInliningDisabledIsIdentical checks that -d=loopinline=0 restores
// the previous inlining decisions exactly, so the mechanism can be turned
// off to bisect a regression.
func TestLoopInliningDisabledIsIdentical(t *testing.T) {
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
	if !strings.Contains(off, "cannot inline mixLoop: function too complex") {
		t.Errorf("with loopinline=0, mixLoop should be reported too complex:\n%s", off)
	}
}

// TestLoopInliningTunables checks that each knob is wired up: turning any
// one of them down individually must lose the inline it is responsible for.
func TestLoopInliningTunables(t *testing.T) {
	testenv.MustHaveGoRun(t)
	t.Parallel()

	for _, tc := range []struct {
		flags   string
		diag    string
		want    bool
		comment string
	}{
		{"", "can inline mixLoop", true, "defaults"},
		{" -d=loopinlinediv=-1", "can inline mixLoop", false, "no discount, no rescue"},
		{" -d=loopinlinecredit=1", "can inline mixLoop", false, "credit capped to nothing"},
		{"", "inlining call to bigLoop", true, "defaults"},
		{" -d=loopinlinefactor=1", "inlining call to bigLoop", false, "call site budget does not grow"},
		{" -d=loopinlinegrowth=1", "inlining call to bigLoop", false, "caller growth allowance exhausted"},
		{" -d=loopinlinebudget=80", "inlining call to bigLoop", false, "callee never gets an inline body"},
	} {
		out := buildLoopInlineTest(t, "-m=2"+tc.flags)
		if got := strings.Contains(out, tc.diag); got != tc.want {
			t.Errorf("gcflags %q (%s): %q found = %v, want %v\n%s",
				"-m=2"+tc.flags, tc.comment, tc.diag, got, tc.want, out)
		}
	}
}
