// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package syscall

// x86 port I/O. The linux port declares Ioperm and Iopl in
// syscall_linux_amd64.go, because the two syscalls exist on x86 only.
// SYS_IOPERM and SYS_IOPL are in the cosmo amd64 syscall table and in no
// other cosmo table. The bodies are zsyscall_linux_amd64.go's, unchanged.
//
// Only a Linux kernel serves them. The Windows emulation
// (runtime.ntSyscallEmulate) has no case for either number, so it answers
// ENOSYS.

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
