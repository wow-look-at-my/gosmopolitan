// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cgroup

import (
	"internal/runtime/syscall/cosmo"
)

// Cosmopolitan uses Linux syscall numbers and Linux errno values on every
// host, so ENOENT here is the Linux one. A host with no /proc answers it for
// these paths, which is the same answer as "not in a cgroup".
const _ENOENT = 2

// sysOpenRead opens path, a NUL-terminated string, for reading.
func sysOpenRead(path *byte) (int, uintptr) {
	return cosmo.Open(path, cosmo.O_RDONLY|cosmo.O_CLOEXEC, 0)
}

func sysClose(fd int) { cosmo.Close(fd) }

func sysRead(fd int, b []byte) (int, uintptr) { return cosmo.Read(fd, b) }

func sysPread(fd int, b []byte, off int64) (int, uintptr) { return cosmo.Pread(fd, b, off) }

// sysNotExist reports whether errno says the file is absent.
func sysNotExist(errno uintptr) bool { return errno == _ENOENT }
