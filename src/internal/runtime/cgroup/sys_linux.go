// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && !cosmo

package cgroup

import (
	"internal/runtime/syscall/linux"
)

// sysOpenRead opens path, a NUL-terminated string, for reading.
func sysOpenRead(path *byte) (int, uintptr) {
	return linux.Open(path, linux.O_RDONLY|linux.O_CLOEXEC, 0)
}

func sysClose(fd int) { linux.Close(fd) }

func sysRead(fd int, b []byte) (int, uintptr) { return linux.Read(fd, b) }

func sysPread(fd int, b []byte, off int64) (int, uintptr) { return linux.Pread(fd, b, off) }

// sysNotExist reports whether errno says the file is absent.
func sysNotExist(errno uintptr) bool { return errno == linux.ENOENT }
