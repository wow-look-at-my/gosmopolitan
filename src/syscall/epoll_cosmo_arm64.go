// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

// The epoll syscalls darwinEpollTrap intercepts beyond the three every
// architecture has. arm64 Linux has no epoll_create or epoll_wait, so those
// two are numbers no trap carries.
const (
	darwinSysEpollCreate = ^uintptr(0)
	darwinSysEpollWait   = ^uintptr(1)
	darwinSysEpollPwait2 = SYS_EPOLL_PWAIT2
)
