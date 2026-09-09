// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package unix

import (
	"internal/strconv"
	"runtime"
	"syscall"
	"unsafe"
)

func Fchmodat(dirfd int, path string, mode uint32, flags int) error {
	if flags&^AT_SYMLINK_NOFOLLOW != 0 {
		return syscall.EINVAL
	}
	if flags == 0 {
		return syscall.Fchmodat(dirfd, path, mode, 0)
	}
	if runtime.GOOS == "darwin" {
		// Apple's own fchmodat takes the flag, so the emulation
		// forwards it (translated) and no detour is needed.
		p, err := syscall.BytePtrFromString(path)
		if err != nil {
			return err
		}
		_, _, e := syscall.Syscall6(fchmodatTrap, uintptr(dirfd),
			uintptr(unsafe.Pointer(p)), uintptr(mode),
			uintptr(AT_SYMLINK_NOFOLLOW), 0, 0)
		if e != 0 {
			return e
		}
		return nil
	}

	// AT_SYMLINK_NOFOLLOW: the Linux fchmodat(2) ABI has no flags
	// argument and silently ignores the request (and fchmodat2 is not
	// wired up). Passing the flag through would chmod the symlink
	// TARGET, the exact escape Root.Chmod uses the flag to prevent. Use
	// the same workaround as GNU libc and musl: open an O_PATH
	// descriptor and chmod it via /proc/self/fd, refusing symlinks.
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
