// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "unsafe"

// The all-threads syscall pair and the credential setters that need it.

//go:uintptrescapes
func runtime_doAllThreadsSyscall(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr)

// AllThreadsSyscall performs a syscall on each OS thread of the Go runtime.
// It first invokes the syscall on one thread.
//
//go:uintptrescapes
func AllThreadsSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	r1, r2, errno := runtime_doAllThreadsSyscall(trap, a1, a2, a3, 0, 0, 0)
	return r1, r2, Errno(errno)
}

// AllThreadsSyscall6 is like [AllThreadsSyscall], but extended to
// arguments.
//
//go:uintptrescapes
func AllThreadsSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	r1, r2, errno := runtime_doAllThreadsSyscall(trap, a1, a2, a3, a4, a5, a6)
	return r1, r2, Errno(errno)
}

func Setegid(egid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETRESGID, setresIgnore, uintptr(egid), setresIgnore); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Seteuid(euid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETRESUID, setresIgnore, uintptr(euid), setresIgnore); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setgid(gid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETGID, uintptr(gid), 0, 0); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setregid(rgid, egid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETREGID, uintptr(rgid), uintptr(egid), 0); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setresgid(rgid, egid, sgid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETRESGID, uintptr(rgid), uintptr(egid), uintptr(sgid)); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setresuid(ruid, euid, suid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETRESUID, uintptr(ruid), uintptr(euid), uintptr(suid)); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setreuid(ruid, euid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETREUID, uintptr(ruid), uintptr(euid), 0); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setuid(uid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETUID, uintptr(uid), 0, 0); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setfsgid(gid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETFSGID, uintptr(gid), 0, 0); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setfsuid(uid int) (err error) {
	if _, _, e1 := AllThreadsSyscall(SYS_SETFSUID, uintptr(uid), 0, 0); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setgroups(gids []int) (err error) {
	n := uintptr(len(gids))
	if n == 0 {
		if _, _, e1 := AllThreadsSyscall(SYS_SETGROUPS, 0, 0, 0); e1 != 0 {
			err = errnoErr(e1)
		}
		return
	}

	a := make([]_Gid_t, len(gids))
	for i, v := range gids {
		a[i] = _Gid_t(v)
	}
	if _, _, e1 := AllThreadsSyscall(SYS_SETGROUPS, n, uintptr(unsafe.Pointer(&a[0])), 0); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}
