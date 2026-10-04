// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

// The epoll syscalls darwinEpollTrap intercepts beyond those every
// architecture has. arm64 Linux has no epoll_create or epoll_wait.
const (
	darwinSysEpollCreate = ^uintptr(0)
	darwinSysEpollWait   = ^uintptr(1)
	darwinSysEpollPwait2 = SYS_EPOLL_PWAIT2
)
