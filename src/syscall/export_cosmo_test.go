// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package syscall

import (
	"unsafe"
)

func Tcgetpgrp(fd int) (pgid int32, err error) {
	_, _, errno := Syscall6(SYS_IOCTL, uintptr(fd), uintptr(TIOCGPGRP), uintptr(unsafe.Pointer(&pgid)), 0, 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return pgid, nil
}

func Tcsetpgrp(fd int, pgid int32) (err error) {
	_, _, errno := Syscall6(SYS_IOCTL, uintptr(fd), uintptr(TIOCSPGRP), uintptr(unsafe.Pointer(&pgid)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// Exports for bigbuf_cosmo_test.go. The test has to live in package
// syscall_test - an in-package test cannot import testing here - so the
// macOS struct conversions need a window.

var (
	DarwinStatfsToLinuxForTest   = darwinStatfsToLinux
	DarwinMntFlagsToLinuxForTest = darwinMntFlagsToLinux
	DarwinUtsnameToLinuxForTest  = darwinUtsnameToLinux
)

const (
	AppleMntRdonlyForTest  = appleMNT_RDONLY
	AppleMntSyncForTest    = appleMNT_SYNCHRONOUS
	AppleMntNoexecForTest  = appleMNT_NOEXEC
	AppleMntNosuidForTest  = appleMNT_NOSUID
	AppleMntNodevForTest   = appleMNT_NODEV
	AppleMntNoatimeForTest = appleMNT_NOATIME

	LinuxStRdonlyForTest  = linuxST_RDONLY
	LinuxStNosuidForTest  = linuxST_NOSUID
	LinuxStNodevForTest   = linuxST_NODEV
	LinuxStNoexecForTest  = linuxST_NOEXEC
	LinuxStSyncForTest    = linuxST_SYNCHRONOUS
	LinuxStNoatimeForTest = linuxST_NOATIME
)
