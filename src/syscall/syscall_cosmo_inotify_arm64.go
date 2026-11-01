// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && arm64

// The arm64 table has no inotify_init number.

package syscall

// The arm64 table has no inotify_init number.

const darwinSysInotifyInit = ^uintptr(2)

// inotifyDup2Trap is dup2's number, which the arm64 table does not have either.
const inotifyDup2Trap = ^uintptr(3)

func InotifyInit() (fd int, err error) {
	return InotifyInit1(0)
}
