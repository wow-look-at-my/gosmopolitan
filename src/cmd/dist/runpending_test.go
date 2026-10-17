// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const pendingHelperEnv = "DIST_RUNPENDING_HELPER"

// TestRunPendingHelper is the command each work runs. It does nothing in a
// normal run.
func TestRunPendingHelper(t *testing.T) {
	mode, file, ok := strings.Cut(os.Getenv(pendingHelperEnv), ":")
	if !ok {
		return
	}
	switch mode {
	case "wait":
		// A parent that died, for example on a test timeout, never writes
		// the file. The wait ends with it rather than outliving it.
		parent := os.Getppid()
		for os.Getppid() == parent {
			if _, err := os.Stat(file); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("the parent exited before the file appeared")
	case "touch":
		if err := os.WriteFile(file, nil, 0o666); err != nil {
			t.Fatal(err)
		}
	}
}

// The first work waits for a file that only the third work writes. With two
// slots, the third work must start when the second ends, while the first is
// still running. A scheduler that frees a slot only when the oldest work ends
// never starts the third, and this test times out.
func TestRunPendingStartsOnAnyEnd(t *testing.T) {
	testenv.MustHaveExec(t)
	t.Serial()
	marker := filepath.Join(t.TempDir(), "marker")
	saved := maxbg
	maxbg = 2
	defer func() { maxbg = saved }()

	var tester tester
	for idx, mode := range []string{"wait", "noop", "touch"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRunPendingHelper$")
		cmd.Env = append(os.Environ(), pendingHelperEnv+"="+mode+":"+marker)
		work := &work{dt: &distTest{name: "work" + string(rune('a'+idx)), heading: "runpending"}, cmd: cmd}
		cmd.Stdout = &work.out
		cmd.Stderr = &work.out
		tester.worklist = append(tester.worklist, work)
	}
	tester.runPending(nil)
	if tester.failed {
		t.Fatal("a work failed")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the third work never ran: %v", err)
	}
}

// pendingWork is a work that runs the helper in mode against file.
func pendingWork(name, mode, file string) *work {
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunPendingHelper$")
	cmd.Env = append(os.Environ(), pendingHelperEnv+"="+mode+":"+file)
	work := &work{dt: &distTest{name: name, heading: "runpending"}, cmd: cmd}
	cmd.Stdout = &work.out
	cmd.Stderr = &work.out
	return work
}

// The first work waits for a file that only the third work writes, and the
// second work waits for the first to end. With two slots, the third work must
// start while the second waits. A scheduler that holds the works behind a
// waiting one never starts the third, and this test times out.
func TestRunPendingAfterWaitsAlone(t *testing.T) {
	testenv.MustHaveExec(t)
	t.Serial()
	marker := filepath.Join(t.TempDir(), "marker")
	saved := maxbg
	maxbg = 2
	defer func() { maxbg = saved }()

	waiting := pendingWork("waiting", "wait", marker)
	behind := pendingWork("behind", "noop", marker)
	behind.after = waiting
	writer := pendingWork("writer", "touch", marker)

	var tester tester
	tester.worklist = []*work{waiting, behind, writer}
	tester.runPending(nil)
	if tester.failed {
		t.Fatal("a work failed")
	}
	waitingEnd := waiting.began.Add(waiting.elapsed)
	if behind.began.Before(waitingEnd) {
		t.Errorf("the work behind began at %v, before the work it waits for ended at %v", behind.began, waitingEnd)
	}
}

// A work marked first starts before the works queued ahead of it.
func TestRunPendingStartsFirstAhead(t *testing.T) {
	testenv.MustHaveExec(t)
	t.Serial()
	marker := filepath.Join(t.TempDir(), "marker")
	saved := maxbg
	maxbg = 1
	defer func() { maxbg = saved }()

	queued := pendingWork("queued", "noop", marker)
	ahead := pendingWork("ahead", "noop", marker)
	ahead.first = true

	var tester tester
	tester.worklist = []*work{queued, ahead}
	tester.runPending(nil)
	if tester.failed {
		t.Fatal("a work failed")
	}
	if !ahead.began.Before(queued.began) {
		t.Errorf("the work marked first began at %v, not before the one queued ahead of it at %v", ahead.began, queued.began)
	}
}

// A work queued alone starts after the works queued before it end, though a
// slot is free beside them.
func TestQueueAloneWaitsForEarlierWorks(t *testing.T) {
	testenv.MustHaveExec(t)
	t.Serial()
	marker := filepath.Join(t.TempDir(), "marker")
	saved := maxbg
	maxbg = 2
	defer func() { maxbg = saved }()

	earlier := pendingWork("earlier", "wait", marker)
	alone := pendingWork("alone", "noop", marker)
	writeMarker := time.AfterFunc(200*time.Millisecond, func() {
		if err := os.WriteFile(marker, nil, 0o666); err != nil {
			t.Error(err)
		}
	})
	defer writeMarker.Stop()

	var tester tester
	tester.queue(earlier, false)
	tester.queue(alone, true)
	tester.runPending(nil)
	if tester.failed {
		t.Fatal("a work failed")
	}
	earlierEnd := earlier.began.Add(earlier.elapsed)
	if alone.began.Before(earlierEnd) {
		t.Errorf("the alone work began at %v, before the earlier work ended at %v", alone.began, earlierEnd)
	}
}

// An early work runs from startEarly, and runPending waits for it without a
// slot. With one slot, the work behind it must start while it waits for the
// file that work writes. An early work that held a slot never lets it start.
func TestStartEarlyHoldsNoSlot(t *testing.T) {
	testenv.MustHaveExec(t)
	t.Serial()
	marker := filepath.Join(t.TempDir(), "marker")
	saved := maxbg
	maxbg = 1
	defer func() { maxbg = saved }()

	var tester tester
	early := pendingWork("early", "wait", marker)
	tester.tests = []distTest{{name: "early", heading: "runpending", early: true, fn: func(*distTest) error {
		tester.worklist = append(tester.worklist, early)
		return nil
	}}}
	tester.startEarly()
	if len(tester.worklist) != 0 {
		t.Fatalf("startEarly left %d works in the worklist, want 0", len(tester.worklist))
	}
	started := tester.started["early"]
	if started == nil || len(started.works) != 1 || started.works[0] != early {
		t.Fatalf("startEarly recorded %+v for the early test, want its one work", started)
	}
	writer := pendingWork("writer", "touch", marker)
	tester.worklist = append(slices.Clone(started.works), writer)
	tester.runPending(nil)
	if tester.failed {
		t.Fatal("a work failed")
	}
	if !early.began.Before(writer.began) {
		t.Errorf("the early work began at %v, not before the work behind it at %v", early.began, writer.began)
	}
}

func TestQueuedFindsTheWaitingWork(t *testing.T) {
	var tester tester
	waiting := &work{dt: &distTest{name: "crypto/...:gofips140-v1.0.0-c2097c7c"}}
	tester.worklist = []*work{waiting}
	if got := tester.queued("crypto/...:gofips140-v1.0.0-c2097c7c"); got != waiting {
		t.Errorf("queued(the waiting test) = %v, want its work", got)
	}
	for _, name := range []string{"", "crypto/...:gofips140-v1.26.0"} {
		if got := tester.queued(name); got != nil {
			t.Errorf("queued(%q) = %v, want nil: no such work waits", name, got)
		}
	}
}

func TestFipsModuleResolvesAliases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "certified.txt"), []byte("v1.0.0-c2097c7c\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "v1.0.0-c2097c7c.zip"), nil, 0o666); err != nil {
		t.Fatal(err)
	}
	for version, want := range map[string]string{
		"certified":       "v1.0.0-c2097c7c",
		"v1.0.0-c2097c7c": "v1.0.0-c2097c7c",
	} {
		if got := fipsModule(dir, version); got != want {
			t.Errorf("fipsModule(%q) = %q, want %q", version, got, want)
		}
	}
}
