// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

// Syscall numbers for arm64.
// Cosmopolitan uses Linux syscall numbers on all platforms.
// These are the arm64/aarch64 Linux syscall numbers.
const (
	SYS_READ          = 63
	SYS_WRITE         = 64
	SYS_OPEN          = 56 // openat on arm64
	SYS_CLOSE         = 57
	SYS_MMAP          = 222
	SYS_MPROTECT      = 226
	SYS_MUNMAP        = 215
	SYS_PREAD64       = 67
	SYS_SCHED_YIELD   = 124
	SYS_NANOSLEEP     = 101
	SYS_GETPID        = 172
	SYS_CLONE         = 220
	SYS_EXIT          = 93
	SYS_KILL          = 129
	SYS_FCNTL         = 25
	SYS_GETCWD        = 17
	SYS_GETTID        = 178
	SYS_FUTEX         = 98
	SYS_SET_TID_ADDRESS = 96
	SYS_EXIT_GROUP    = 94
	SYS_EPOLL_CTL     = 21
	SYS_OPENAT        = 56
	SYS_EPOLL_PWAIT   = 22
	SYS_EVENTFD2      = 19
	SYS_EPOLL_CREATE1 = 20
	SYS_PRCTL         = 167
	SYS_EPOLL_PWAIT2  = 441
)

// EpollEvent is the epoll_event structure. Unlike x86-64, the Linux
// kernel does not pack the struct on arm64: it is 16 bytes with Data at
// offset 8.
type EpollEvent struct {
	Events uint32
	_pad   uint32
	Data   [8]byte // to match amd64
}
