// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

// Package cosmo provides the syscall primitives required for the runtime
// on Cosmopolitan Libc (GOOS=cosmo).
//
// Cosmopolitan Libc provides a portable syscall interface that abstracts
// away differences between Linux, macOS, Windows, and FreeBSD. It uses
// Linux-style syscall numbers and calling conventions.
package cosmo

// Epoll constants
const (
	EPOLLIN       = 0x1
	EPOLLOUT      = 0x4
	EPOLLERR      = 0x8
	EPOLLHUP      = 0x10
	EPOLLRDHUP    = 0x2000
	EPOLLET       = 0x80000000
	EPOLL_CLOEXEC = 0x80000
	EPOLL_CTL_ADD = 0x1
	EPOLL_CTL_DEL = 0x2
	EPOLL_CTL_MOD = 0x3
)

// Eventfd constants
const (
	EFD_CLOEXEC  = 0x80000
	EFD_NONBLOCK = 0x800
)

// File open constants
const (
	O_RDONLY    = 0x0
	O_WRONLY    = 0x1
	O_RDWR      = 0x2
	O_CREAT     = 0x40
	O_TRUNC     = 0x200
	O_NONBLOCK  = 0x800
	O_CLOEXEC   = 0x80000
	O_LARGEFILE = 0x0 // Not needed on 64-bit
)

const AT_FDCWD = -0x64

// EpollEvent is defined in the per-architecture defs_cosmo_GOARCH.go
// files: the Linux kernel packs struct epoll_event on x86-64 (12 bytes)
// but aligns it naturally everywhere else (16 bytes on arm64).
