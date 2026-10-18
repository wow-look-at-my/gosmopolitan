// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package work

import (
	"os/exec"
	"testing"
	"time"
)

func TestLowerToolPriority(t *testing.T) {
	own, err := niceOf(0)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()

	if err := lowerToolPriority(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	got, err := niceOf(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if want := min(own+toolNiceIncrement, maxNice); got != want {
		t.Errorf("child nice = %d, want %d (own %d)", got, want, own)
	}
}

// A tool can start below the target already, when the thread that started it
// had a lower priority than the one that reads the go command's own. Lowering
// it then leaves it where it is, and raising it would need privilege.
func TestLowerToolPriorityKeepsALowerTool(t *testing.T) {
	cmd := exec.Command("nice", "-n", "19", "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()
	// nice sets the value and then execs sleep, so wait for the value to land.
	for range 1000 {
		if got, err := niceOf(cmd.Process.Pid); err == nil && got == maxNice {
			break
		}
		time.Sleep(time.Millisecond)
	}

	if err := lowerToolPriority(cmd.Process.Pid); err != nil {
		t.Fatalf("lowering a tool already at nice %d: %v", maxNice, err)
	}
	got, err := niceOf(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if got != maxNice {
		t.Errorf("child nice = %d, want it left at %d", got, maxNice)
	}
}

func TestLowerToolPriorityExited(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := lowerToolPriority(cmd.Process.Pid); err != nil {
		t.Errorf("lowering an exited process: %v", err)
	}
}
