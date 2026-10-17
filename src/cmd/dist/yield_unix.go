// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"os/exec"
	"syscall"
)

// startYielding starts cmd at a lower priority than dist's own. The processes
// cmd starts take the priority from it.
func startYielding(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10); err != nil {
		errprintf("dist: %s runs at dist's priority, beside the early tests: setpriority: %v\n", cmd.Path, err)
	}
	return nil
}
