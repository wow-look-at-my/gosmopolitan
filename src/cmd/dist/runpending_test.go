// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
