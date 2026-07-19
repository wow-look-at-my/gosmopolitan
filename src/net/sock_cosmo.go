// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package net

import (
	"internal/syscall/unix"
	"syscall"
)

// Cosmopolitan stores the backlog like Linux:
//
//   - uint16 in kernel version < 4.1,
//   - uint32 in kernel version >= 4.1
//
// Truncate number to avoid wrapping.
//
// See issue 5030 and 41470.
func maxAckBacklog(n int) int {
	size := 16
	if unix.KernelVersionGE(4, 1) {
		size = 32
	}

	var max uint = 1<<size - 1
	if uint(n) > max {
		n = int(max)
	}
	return n
}

func maxListenerBacklog() int {
	// Try Linux's /proc interface first (works when running on Linux)
	fd, err := open("/proc/sys/net/core/somaxconn")
	if err != nil {
		return syscall.SOMAXCONN
	}
	defer fd.close()
	l, ok := fd.readLine()
	if !ok {
		return syscall.SOMAXCONN
	}
	f := getFields(l)
	n, _, ok := dtoi(f[0])
	if n == 0 || !ok {
		return syscall.SOMAXCONN
	}

	if n > 1<<16-1 {
		return maxAckBacklog(n)
	}
	return n
}
