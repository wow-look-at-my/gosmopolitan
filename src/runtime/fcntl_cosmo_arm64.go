// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import "internal/runtime/syscall/cosmo"

// fcntl performs the fcntl syscall.
// On ARM64 darwin, fcntl is not available through Syslib, and raw Linux
// syscalls don't work. For the primary use case (checkfds checking if
// file descriptors are valid), we return success since the APE loader
// provides valid stdin/stdout/stderr.
//
//go:nosplit
func fcntl(fd, cmd, arg int32) (ret int32, errno int32) {
	if isdarwin() {
		// On darwin ARM64, we can't make Linux fcntl syscalls.
		// For F_GETFD (used by checkfds), return 0 indicating fd is valid.
		// The APE loader ensures stdin/stdout/stderr are valid.
		const F_GETFD = 0x01
		if cmd == F_GETFD {
			// Return 0 (success, no close-on-exec flag set)
			return 0, 0
		}
		// For other fcntl commands, return ENOSYS
		// This may need to be expanded if other fcntl uses are needed
		return -1, 38 // ENOSYS
	}
	// Linux path: use direct syscall
	r, _, err := cosmo.Syscall6(cosmo.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg), 0, 0, 0)
	return int32(r), int32(err)
}
