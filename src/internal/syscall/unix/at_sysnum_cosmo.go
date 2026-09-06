// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package unix

import "syscall"

const (
	unlinkatTrap   uintptr = syscall.SYS_UNLINKAT
	openatTrap     uintptr = syscall.SYS_OPENAT
	readlinkatTrap uintptr = syscall.SYS_READLINKAT
	mkdiratTrap    uintptr = syscall.SYS_MKDIRAT
	fchownatTrap   uintptr = syscall.SYS_FCHOWNAT
	linkatTrap     uintptr = syscall.SYS_LINKAT
	symlinkatTrap  uintptr = syscall.SYS_SYMLINKAT
	renameatTrap   uintptr = syscall.SYS_RENAMEAT
	fchmodatTrap   uintptr = syscall.SYS_FCHMODAT
)

const (
	AT_EACCESS          = 0x200
	AT_FDCWD            = -0x64
	AT_REMOVEDIR        = 0x200
	AT_SYMLINK_NOFOLLOW = 0x100

	UTIME_OMIT = 0x3ffffffe

	O_PATH = 0x200000
)
