// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package unix

import (
	"syscall"
	_ "unsafe" // for go:linkname
)

// Implemented in the runtime package.
//
//go:linkname fcntl runtime.fcntl
func fcntl(fd int32, cmd int32, arg int32) (int32, int32)

// Fcntl sends a duplication through package syscall, which keeps the new
// descriptor a name of any inotify instance the old one names.
func Fcntl(fd int, cmd int, arg int) (int, error) {
	if cmd == syscall.F_DUPFD || cmd == syscall.F_DUPFD_CLOEXEC {
		val, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg))
		if errno != 0 {
			return -1, errno
		}
		return int(val), nil
	}
	val, errno := fcntl(int32(fd), int32(cmd), int32(arg))
	if val == -1 {
		return int(val), syscall.Errno(errno)
	}
	return int(val), nil
}
