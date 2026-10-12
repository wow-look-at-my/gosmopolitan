// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

package syscall

import "unsafe"

// The amd64 members of the time and clock group.

func Pause() (err error) {
	_, _, e1 := Syscall(SYS_PAUSE, 0, 0, 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Select(nfd int, r *FdSet, w *FdSet, e *FdSet, timeout *Timeval) (n int, err error) {
	r0, _, e1 := Syscall6(SYS_SELECT, uintptr(nfd), uintptr(unsafe.Pointer(r)), uintptr(unsafe.Pointer(w)), uintptr(unsafe.Pointer(e)), uintptr(unsafe.Pointer(timeout)), 0)
	n = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Time(t *Time_t) (tt Time_t, err error) {
	return cosmoTime(t)
}

func Ustat(dev int, ubuf *Ustat_t) (err error) {
	_, _, e1 := Syscall(SYS_USTAT, uintptr(dev), uintptr(unsafe.Pointer(ubuf)), 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Utime(path string, buf *Utimbuf) (err error) {
	var _p0 *byte
	_p0, err = BytePtrFromString(path)
	if err != nil {
		return
	}
	_, _, e1 := Syscall(SYS_UTIME, uintptr(unsafe.Pointer(_p0)), uintptr(unsafe.Pointer(buf)), 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}
