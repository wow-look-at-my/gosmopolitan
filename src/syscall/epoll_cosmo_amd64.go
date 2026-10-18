// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

// The epoll syscalls darwinEpollTrap intercepts beyond those every
// architecture has.
const (
	darwinSysEpollCreate = SYS_EPOLL_CREATE
	darwinSysEpollWait   = SYS_EPOLL_WAIT
	darwinSysEpollPwait2 = 441
)
