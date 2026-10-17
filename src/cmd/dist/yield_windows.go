// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os/exec"
	"syscall"
)

// belowNormalPriorityClass is BELOW_NORMAL_PRIORITY_CLASS. A process of this
// class gives it to the processes it creates.
const belowNormalPriorityClass = 0x00004000

// startYielding starts cmd at a lower priority than dist's own. The processes
// cmd starts take the priority from it.
func startYielding(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}
	cmd.SysProcAttr.CreationFlags |= belowNormalPriorityClass
	return cmd.Start()
}
