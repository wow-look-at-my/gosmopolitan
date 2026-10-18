// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package work

import (
	"errors"
	"syscall"
)

// maxNice is the lowest scheduling priority every one of these kernels accepts.
const maxNice = 19

// lowerToolPriority sets the nice value of process pid to
// toolNiceIncrement above the go command's own, at most maxNice.
// A process that has already exited has nothing to lower.
//
// On Linux a nice value belongs to one thread. The tool takes the value of
// the thread that started it, and niceOf(0) can read a different thread, so
// the tool can already sit below the target. Raising it back needs privilege.
func lowerToolPriority(pid int) error {
	own, err := niceOf(0)
	if err != nil {
		return err
	}
	target := min(own+toolNiceIncrement, maxNice)
	current, err := niceOf(pid)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	if current >= target {
		return nil
	}
	err = syscall.Setpriority(syscall.PRIO_PROCESS, pid, target)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
