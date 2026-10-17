// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// A command started to yield runs at yieldNice, above dist's own.
func TestStartYieldingLowersPriority(t *testing.T) {
	testenv.MustHaveExec(t)
	marker := filepath.Join(t.TempDir(), "marker")
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunPendingHelper$")
	cmd.Env = append(os.Environ(), pendingHelperEnv+"=wait:"+marker)
	if err := startYielding(cmd); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer os.WriteFile(marker, nil, 0o666)

	prio, err := syscall.Getpriority(syscall.PRIO_PROCESS, cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	nice := prio
	if runtime.GOOS == "linux" {
		// The Linux system call answers 20 less the nice value.
		nice = 20 - prio
	}
	if nice != yieldNice {
		t.Errorf("the yielding command runs at nice %d, want %d", nice, yieldNice)
	}
}
