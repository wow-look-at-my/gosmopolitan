// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "unsafe"

// The inotify group. GOOS=cosmo presents the Linux ABI, so a program written
// against the linux port names these, and the linux port declares them. In
// syscall_linux.go. Cosmo does not build that.

const (
	IN_ACCESS        = 0x1
	IN_ALL_EVENTS    = 0xfff
	IN_ATTRIB        = 0x4
	IN_CLOEXEC       = 0x80000
	IN_CLOSE         = 0x18
	IN_CLOSE_NOWRITE = 0x10
	IN_CLOSE_WRITE   = 0x8
	IN_CREATE        = 0x100
	IN_DELETE        = 0x200
	IN_DELETE_SELF   = 0x400
	IN_DONT_FOLLOW   = 0x2000000
	IN_EXCL_UNLINK   = 0x4000000
	IN_IGNORED       = 0x8000
	IN_ISDIR         = 0x40000000
	IN_MASK_ADD      = 0x20000000
	IN_MODIFY        = 0x2
	IN_MOVE          = 0xc0
	IN_MOVED_FROM    = 0x40
	IN_MOVED_TO      = 0x80
	IN_MOVE_SELF     = 0x800
	IN_NONBLOCK      = 0x800
	IN_ONESHOT       = 0x80000000
	IN_ONLYDIR       = 0x1000000
	IN_OPEN          = 0x20
	IN_Q_OVERFLOW    = 0x4000
	IN_UNMOUNT       = 0x2000
)

func InotifyAddWatch(fd int, pathname string, mask uint32) (watchdesc int, err error) {
	var _p0 *byte
	_p0, err = BytePtrFromString(pathname)
	if err != nil {
		return
	}
	r0, _, e1 := Syscall(SYS_INOTIFY_ADD_WATCH, uintptr(fd), uintptr(unsafe.Pointer(_p0)), uintptr(mask))
	watchdesc = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func InotifyInit1(flags int) (fd int, err error) {
	r0, _, e1 := RawSyscall(SYS_INOTIFY_INIT1, uintptr(flags), 0, 0)
	fd = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func InotifyRmWatch(fd int, watchdesc uint32) (success int, err error) {
	r0, _, e1 := RawSyscall(SYS_INOTIFY_RM_WATCH, uintptr(fd), uintptr(watchdesc), 0)
	success = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}
