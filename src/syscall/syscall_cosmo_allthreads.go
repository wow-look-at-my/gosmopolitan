// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "unsafe"

// The all-threads syscall pair and the credential setters that need it.
// GOOS=cosmo satisfies the linux build tag, so a program written against the
// linux port names these, and the linux port declares them in
// syscall_linux.go, which cosmo does not build.
//
// A credential change made on one thread is a change the process did not
// make: every other M keeps the old identity while the call reports
// success. The runtime driver runs the call on every M on a Linux host. On
// a darwin or NT host it runs the call once, which is process-wide there.

//go:uintptrescapes
func runtime_doAllThreadsSyscall(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr)

// AllThreadsSyscall performs a syscall on each OS thread of the Go
// runtime. It first invokes the syscall on one thread. Should that
// invocation fail, it returns immediately with the error status.
// Otherwise, it invokes the syscall on all of the remaining threads
// in parallel. It will terminate the program if it observes any
// invoked syscall's return value differs from that of the first
// invocation.
//
//go:uintptrescapes
func AllThreadsSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	r1, r2, errno := runtime_doAllThreadsSyscall(trap, a1, a2, a3, 0, 0, 0)
	return r1, r2, Errno(errno)
}

// AllThreadsSyscall6 is like [AllThreadsSyscall], but extended to six
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
