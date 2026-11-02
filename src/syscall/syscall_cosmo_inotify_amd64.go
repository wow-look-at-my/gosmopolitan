// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

// InotifyInit has a syscall number in the amd64 table and none in the arm64 table.

package syscall

// InotifyInit has a syscall number in the amd64 table and none in the arm64 table.

const darwinSysInotifyInit = SYS_INOTIFY_INIT

// inotifyDup2Trap is dup2's number, which the arm64 table does not have.
const inotifyDup2Trap = SYS_DUP2

func InotifyInit() (fd int, err error) {
	r0, _, e1 := RawSyscall(SYS_INOTIFY_INIT, 0, 0, 0)
	fd = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}
