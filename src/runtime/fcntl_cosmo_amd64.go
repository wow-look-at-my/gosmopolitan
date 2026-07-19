// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import "internal/runtime/syscall/cosmo"

//go:nosplit
func fcntl(fd, cmd, arg int32) (ret int32, errno int32) {
	if iswindows() {
		// Consult the NT fd table (os_cosmo_nt_fd.go). Slots 0-2
		// are seeded open at boot, which keeps checkfds
		// (fds_unix.go) from trying to open /dev/null through the
		// runtime's raw-syscall open.
		return ntFcntl(fd, cmd, arg)
	}
	r, _, err := cosmo.Syscall6(cosmo.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg), 0, 0, 0)
	return int32(r), int32(err)
}
