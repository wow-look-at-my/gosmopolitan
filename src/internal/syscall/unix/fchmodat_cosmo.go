// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package unix

import (
	"internal/strconv"
	"syscall"
)

func Fchmodat(dirfd int, path string, mode uint32, flags int) error {
	if flags&^AT_SYMLINK_NOFOLLOW != 0 {
		return syscall.EINVAL
	}
	if flags == 0 {
		return syscall.Fchmodat(dirfd, path, mode, 0)
	}

	// AT_SYMLINK_NOFOLLOW: the cosmo syscall layer speaks the Linux
	// fchmodat(2) ABI, which silently ignores the flag (and fchmodat2
	// is not wired up). Passing the flag through would chmod the
	// symlink TARGET, the exact escape Root.Chmod uses the flag to
	// prevent. Use the same workaround as GNU libc and musl: open an
	// O_PATH descriptor and chmod it via /proc/self/fd, refusing
	// symlinks. On hosts without procfs (macOS) this reports
	// EOPNOTSUPP instead of silently following; the darwin syscall
	// emulation does not implement the chmod family yet anyway.
	fd, err := Openat(dirfd, path, O_PATH|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	procPath := "/proc/self/fd/" + strconv.Itoa(fd)

	// Check to see if this file is a symlink.
	// (We passed O_NOFOLLOW above, but O_PATH|O_NOFOLLOW will open a symlink.)
	var st syscall.Stat_t
	if err := syscall.Stat(procPath, &st); err != nil {
		if err == syscall.ENOENT {
			// /proc has probably not been mounted. Give up.
			return syscall.EOPNOTSUPP
		}
		return err
	}
	if st.Mode&syscall.S_IFMT == syscall.S_IFLNK {
		// fchmodat on the proc FD for a symlink apparently gives inconsistent
		// results, so just refuse to try.
		return syscall.EOPNOTSUPP
	}

	return syscall.Fchmodat(AT_FDCWD, procPath, mode, 0)
}
