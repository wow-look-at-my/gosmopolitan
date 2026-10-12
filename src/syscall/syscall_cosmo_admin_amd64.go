// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

package syscall

// x86 port I/O. The linux port declares Ioperm and Iopl in
// syscall_linux_amd64.go, because both syscalls exist on x86 only.

func Ioperm(from int, num int, on int) (err error) {
	_, _, e1 := Syscall(SYS_IOPERM, uintptr(from), uintptr(num), uintptr(on))
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Iopl(level int) (err error) {
	_, _, e1 := Syscall(SYS_IOPL, uintptr(level), 0, 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}
