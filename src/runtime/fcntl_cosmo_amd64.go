// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import "internal/runtime/syscall/cosmo"

//go:nosplit
func fcntl(fd, cmd, arg int32) (ret int32, errno int32) {
	if iswindows() {
		// Wave 1 has no fd table; fds 0-2 map to the NT std handles
		// and are always "open". F_GETFD's boot-path caller is
		// checkfds (fds_unix.go), which throws on any errno other
		// than EBADF - report the std fds open with no flags.
		// Everything else stays ENOSYS.
		if cmd == 0x01 /* F_GETFD */ && fd >= 0 && fd < 3 {
			return 0, 0
		}
		return -1, 38 // ENOSYS
	}
	r, _, err := cosmo.Syscall6(cosmo.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg), 0, 0, 0)
	return int32(r), int32(err)
}
