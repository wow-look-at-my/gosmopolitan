// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build aix || darwin || dragonfly || freebsd || netbsd || openbsd || solaris

package work

import "syscall"

// niceOf returns the nice value of process pid, or of the caller for 0.
func niceOf(pid int) (int, error) {
	return syscall.Getpriority(syscall.PRIO_PROCESS, pid)
}
