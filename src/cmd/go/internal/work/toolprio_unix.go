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
// toolNiceIncrement above that process's own, at most maxNice.
// A process that has already exited has nothing to lower, and one this
// go command may not lower keeps the priority it has.
func lowerToolPriority(pid int) error {
	// The tool's own nice, not the caller's: a nice value is per thread,
	// so the thread that forked the tool need not share the nice of the
	// thread that reads it here. Raising the tool from where it already
	// is makes the call an increase in nice, which no privilege gates.
	own, err := niceOf(pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	err = syscall.Setpriority(syscall.PRIO_PROCESS, pid, min(own+toolNiceIncrement, maxNice))
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return nil
	}
	return err
}
