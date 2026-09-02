// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package syscall

import "unsafe"

// The arm64 members of the time and clock group. The arm64 table has no
// SYS_PAUSE and no SYS_SELECT, so the arm64 linux port builds Pause on
// SYS_PPOLL and Select on SYS_PSELECT6. This file copies that shape.
// The arm64 table also has no SYS_USTAT, so Ustat stays amd64 only.

// Fstatat is exported on arm64 only. The amd64 port keeps the same call
// unexported and reaches it through Stat and Lstat.
func Fstatat(fd int, path string, stat *Stat_t, flags int) error {
	return fstatat(fd, path, stat, flags)
}

// Pause waits on an empty descriptor set with no timeout. A signal ends
// the wait, which is what pause(2) promises.
func Pause() error {
	_, _, e1 := Syscall6(SYS_PPOLL, 0, 0, 0, 0, 0, 0)
	if e1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

// Select converts the timeout to a Timespec and passes a nil signal
// mask. The final argument of SYS_PSELECT6 is that mask.
func Select(nfd int, r *FdSet, w *FdSet, e *FdSet, timeout *Timeval) (n int, err error) {
	var ts *Timespec
	if timeout != nil {
		ts = &Timespec{Sec: timeout.Sec, Nsec: timeout.Usec * 1000}
	}
	r0, _, e1 := Syscall6(SYS_PSELECT6, uintptr(nfd), uintptr(unsafe.Pointer(r)), uintptr(unsafe.Pointer(w)), uintptr(unsafe.Pointer(e)), uintptr(unsafe.Pointer(ts)), 0)
	n = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Time(t *Time_t) (Time_t, error) {
	return cosmoTime(t)
}

func Utime(path string, buf *Utimbuf) error {
	if buf == nil {
		return Utimes(path, nil)
	}
	tv := []Timeval{
		{Sec: buf.Actime},
		{Sec: buf.Modtime},
	}
	return Utimes(path, tv)
}
