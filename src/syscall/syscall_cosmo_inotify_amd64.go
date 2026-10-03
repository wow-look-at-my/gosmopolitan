// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package syscall

// InotifyInit has a syscall number in the amd64 table and none in the arm64
// table. The arm64 file builds the same name on InotifyInit1.

func InotifyInit() (fd int, err error) {
	r0, _, e1 := RawSyscall(SYS_INOTIFY_INIT, 0, 0, 0)
	fd = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}
