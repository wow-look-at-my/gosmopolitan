// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import (
	"strconv"
	"unsafe"
)

// Time, clock and file-timestamp calls that the linux port declares on
// every architecture. The linux port keeps the architecture-specific
// members of this group in syscall_cosmo_time_amd64.go and
// syscall_cosmo_time_arm64.go.

func Adjtimex(buf *Timex) (state int, err error) {
	r0, _, e1 := Syscall(SYS_ADJTIMEX, uintptr(unsafe.Pointer(buf)), 0, 0)
	state = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Settimeofday(tv *Timeval) (err error) {
	_, _, e1 := RawSyscall(SYS_SETTIMEOFDAY, uintptr(unsafe.Pointer(tv)), 0, 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Sysinfo(info *Sysinfo_t) (err error) {
	_, _, e1 := RawSyscall(SYS_SYSINFO, uintptr(unsafe.Pointer(info)), 0, 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Times(tms *Tms) (ticks uintptr, err error) {
	r0, _, e1 := RawSyscall(SYS_TIMES, uintptr(unsafe.Pointer(tms)), 0, 0)
	ticks = uintptr(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

// Time reads the wall clock through Gettimeofday. The amd64 linux port
// reads it the same way, through its own private gettimeofday helper.
func Time(t *Time_t) (Time_t, error) {
	var tv Timeval
	err := Gettimeofday(&tv)
	if err != nil {
		return 0, err
	}
	if t != nil {
		*t = Time_t(tv.Sec)
	}
	return Time_t(tv.Sec), nil
}

func Futimesat(dirfd int, path string, tv []Timeval) (err error) {
	if len(tv) != 2 {
		return EINVAL
	}
	return futimesat(dirfd, path, (*[2]Timeval)(unsafe.Pointer(&tv[0])))
}

// Futimes names the descriptor through /proc, the way glibc and the
// linux port do. That path resolves on a Linux host only.
func Futimes(fd int, tv []Timeval) (err error) {
	return Utimes("/proc/self/fd/"+strconv.Itoa(fd), tv)
}
