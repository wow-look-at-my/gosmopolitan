// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"os/exec"
	"syscall"
)

// yieldNice is the nice value of a command that yields. At nice 10 a thread
// weighs 110 against 1024 for a thread at nice 0. Twenty such threads on four
// CPUs still took a third of each, and an early test ran at half speed past
// its deadline. At nice 19 a thread weighs 15.
const yieldNice = 19

// startYielding starts cmd at a lower priority than dist's own. The processes
// cmd starts take the priority from it.
func startYielding(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, yieldNice); err != nil {
		errprintf("dist: %s runs at dist's priority, beside the early tests: setpriority: %v\n", cmd.Path, err)
	}
	return nil
}
