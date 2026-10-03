// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package cosmo

// Syscall numbers for amd64.
// Cosmopolitan uses Linux syscall numbers on all platforms.
const (
	SYS_READ            = 0
	SYS_WRITE           = 1
	SYS_OPEN            = 2
	SYS_CLOSE           = 3
	SYS_MMAP            = 9
	SYS_MPROTECT        = 10
	SYS_MUNMAP          = 11
	SYS_PREAD64         = 17
	SYS_SCHED_YIELD     = 24
	SYS_NANOSLEEP       = 35
	SYS_GETPID          = 39
	SYS_CLONE           = 56
	SYS_EXIT            = 60
	SYS_KILL            = 62
	SYS_FCNTL           = 72
	SYS_GETCWD          = 79
	SYS_GETTID          = 186
	SYS_FUTEX           = 202
	SYS_SET_TID_ADDRESS = 218
	SYS_EXIT_GROUP      = 231
	SYS_EPOLL_CTL       = 233
	SYS_OPENAT          = 257
	SYS_EPOLL_PWAIT     = 281
	SYS_EVENTFD2        = 290
	SYS_EPOLL_CREATE1   = 291
	SYS_PRCTL           = 157
	SYS_EPOLL_PWAIT2    = 441
)

// EpollEvent is the epoll_event structure. The Linux kernel declares it
// __attribute__((packed)) on x86-64, so there is no padding between
// Events and Data.
type EpollEvent struct {
	Events uint32
	Data   [8]byte // unaligned uintptr
}
