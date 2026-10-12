// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import "syscall"

// niceOf returns the nice value of process pid, or of the caller for 0.
// Linux's getpriority system call returns 20 minus the nice value.
func niceOf(pid int) (int, error) {
	prio, err := syscall.Getpriority(syscall.PRIO_PROCESS, pid)
	if err != nil {
		return 0, err
	}
	return 20 - prio, nil
}
