// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package work

import (
	"os/exec"
	"testing"
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

func TestLowerToolPriorityExited(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := lowerToolPriority(cmd.Process.Pid); err != nil {
		t.Errorf("lowering an exited process: %v", err)
	}
}
