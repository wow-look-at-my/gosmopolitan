// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// FcntlFlock performs a fcntl syscall for the [F_GETLK], [F_SETLK] or [F_SETLKW] command.
func FcntlFlock(fd uintptr, cmd int, lk *Flock_t) error {
	if cosmo.Darwin() {
		return darwinFcntlFlock(fd, cmd, lk)
	}
	_, _, errno := Syscall(SYS_FCNTL, fd, uintptr(cmd), uintptr(unsafe.Pointer(lk)))
	if errno == 0 {
		return nil
	}
	return errno
}

// darwinFcntlFlock hands the emulation an Apple-shaped record. Apple's
// struct flock orders its fields differently and is eight bytes shorter,
// and every lock type carries a different number, so the record travels
// converted the way statfs and utsname do. The command number stays
// Linux's, and the emulation translates that.
func darwinFcntlFlock(fd uintptr, cmd int, lk *Flock_t) error {
	t, ok := cosmo.DarwinLockType(lk.Type)
	if !ok {
		return EINVAL
	}
	af := cosmo.DarwinFlock{
		Start:  lk.Start,
		Len:    lk.Len,
		Pid:    lk.Pid,
		Type:   t,
		Whence: lk.Whence,
	}
	_, _, errno := Syscall(SYS_FCNTL, fd, uintptr(cmd), uintptr(unsafe.Pointer(&af)))
	if errno != 0 {
		return errno
	}
	// F_GETLK answers through the record. The other two leave it as it
	// was sent, so the trip back costs nothing and says nothing new.
	t, ok = cosmo.LinuxLockType(af.Type)
	if !ok {
		return EINVAL
	}
	lk.Start = af.Start
	lk.Len = af.Len
	lk.Pid = af.Pid
	lk.Type = t
	lk.Whence = af.Whence
	return nil
}
