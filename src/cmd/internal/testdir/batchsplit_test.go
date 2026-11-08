// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package testdir_test

import (
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"
	"time"
)

// TestEligibleRunHeader: the first line names the action. Most run programs
// have a blank line after it.
func TestEligibleRunHeader(t *testing.T) {
	cases := map[string]bool{
		"// run\n\n// Copyright.\n\npackage main\n\nfunc main() {\n}\n":    true,
		"// run\n// Copyright.\n\npackage main\n\nfunc main() {\n}\n":      true,
		"//run\n\npackage main\n\nfunc main() {\n}\n":                      true,
		"// skip\n// run\n\npackage main\n\nfunc main() {\n}\n":            false,
		"// runoutput\n\npackage main\n\nfunc main() {\n}\n":               false,
		"// run -gcflags=-l\n\npackage main\n\nfunc main() {\n}\n":         false,
		"// run\n\n//go:build amd64\n\npackage main\n\nfunc main() {\n}\n": false,
		"// compile\n\n// run\n\npackage main\n\nfunc main() {\n}\n":       false,
		"// run\n\npackage p\n\nfunc main() {\n}\n":                        false,
	}
	for src, want := range cases {
		if _, got := eligible(src); got != want {
			t.Errorf("eligible(%q) = %v, want %v", src, got, want)
		}
	}
}

// TestSplitBatch: a program counts as finished only when the next marker
// follows its output. A process that dies inside a program names it.
func TestSplitBatch(t *testing.T) {
	const nonce = "nonce"
	whole := "init noise\nnonce prog0\nzero\nnonce prog1\none\nnonce .\n"
	finished, started := splitBatch([]byte(whole), nonce)
	want := map[string][]byte{"prog0": []byte("zero\n"), "prog1": []byte("one\n")}
	if !equalOutputs(finished, want) || started != "" {
		t.Fatalf("whole run: finished %q, started %q; want %q and nothing started", finished, started, want)
	}

	died := "nonce prog0\nzero\nnonce prog1\npanic: half\n"
	finished, started = splitBatch([]byte(died), nonce)
	want = map[string][]byte{"prog0": []byte("zero\n")}
	if !equalOutputs(finished, want) || started != "prog1" {
		t.Fatalf("died in prog1: finished %q, started %q; want %q and prog1 started", finished, started, want)
	}

	finished, started = splitBatch([]byte("panic in init\n"), nonce)
	if len(finished) != 0 || started != "" {
		t.Fatalf("died before main: finished %q, started %q; want nothing", finished, started)
	}
}

// TestSplitInits: a run with no programs names what each program printed in
// its package initialization, and the program whose initialization ended the
// process.
func TestSplitInits(t *testing.T) {
	const nonce = "nonce"
	whole := "noncei prog0\nnoncei prog1\ninitv0\nnoncei prog2\nnonce .\n"
	inits, _, finished := splitInits([]byte(whole), nonce)
	want := map[string][]byte{"prog1": []byte("initv0\n")}
	if !equalOutputs(inits, want) || !finished {
		t.Fatalf("whole run: inits %q, finished %v; want %q and finished", inits, finished, want)
	}

	died := "noncei prog0\nnoncei prog1\n"
	_, last, finished := splitInits([]byte(died), nonce)
	if finished || last != "prog1" {
		t.Fatalf("died in prog1's init: last %q, finished %v; want prog1, not finished", last, finished)
	}

	// An init mark is not a program marker.
	finishedProgs, started := splitBatch([]byte(whole), nonce)
	if len(finishedProgs) != 0 || started != "" {
		t.Fatalf("splitBatch read init marks as programs: %q, started %q", finishedProgs, started)
	}
}

// TestRunWatchedEndsAStuckProcess: a process that prints no new mark for the
// limit is ended, and what it printed comes back.
func TestRunWatchedEndsAStuckProcess(t *testing.T) {
	if os.Getenv("TESTDIR_BATCH_STUCK") == "1" {
		fmt.Println("mark one")
		time.Sleep(time.Hour)
	}
	const wait = time.Second
	argv := []string{os.Args[0], "-test.run=^TestRunWatchedEndsAStuckProcess$"}
	t.Setenv("TESTDIR_BATCH_STUCK", "1")
	start := time.Now()
	out := runWatched(argv, []byte("mark "), wait)
	if !strings.Contains(string(out), "mark one\n") {
		t.Fatalf("runWatched lost the output: %q", out)
	}
	if elapsed := time.Since(start); elapsed > time.Minute {
		t.Fatalf("runWatched took %v to end a process stuck for its limit of %v", elapsed, wait)
	}
}

func equalOutputs(got, want map[string][]byte) bool {
	return maps.EqualFunc(got, want, func(left, right []byte) bool { return string(left) == string(right) })
}
