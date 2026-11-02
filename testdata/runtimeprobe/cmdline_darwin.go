// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"syscall"
	"unsafe"
)

// darwinSysctl reads one macOS sysctl by its numeric MIB. A host-GOOS
// darwin build reaches it through package syscall.
func darwinSysctl(numbers ...uint32) ([]byte, error) {
	mib := make([]int32, len(numbers))
	for idx, num := range numbers {
		mib[idx] = int32(num)
	}
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
