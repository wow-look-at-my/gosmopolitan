// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// darwinProcArgs reads this process's argv the way ps reads it on macOS.
// A host-GOOS darwin build reaches the same sysctl through package syscall.
func darwinProcArgs() ([]byte, error) {
	const ctlKern, kernProcArgs2 = 1, 49
	mib := [3]int32{ctlKern, kernProcArgs2, int32(os.Getpid())}
	var size uintptr
	if err := sysctl(mib[:], nil, &size); err != nil {
		return nil, err
	}
	buf := make([]byte, size)
	if err := sysctl(mib[:], &buf[0], &size); err != nil {
		return nil, err
	}
	return buf[:size], nil
}

func sysctl(mib []int32, out *byte, size *uintptr) error {
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)),
		uintptr(unsafe.Pointer(out)), uintptr(unsafe.Pointer(size)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
