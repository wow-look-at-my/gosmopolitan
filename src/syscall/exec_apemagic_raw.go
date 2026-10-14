// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || linux

package syscall

import "unsafe"

// hasAPEMagic reports whether the file at path starts with an APE header.
// The forked child calls it, so it must not allocate.
//
//go:nosplit
//go:norace
func hasAPEMagic(path *byte) bool {
	fdcwd := _AT_FDCWD
	fd, _, errno := RawSyscall6(SYS_OPENAT, uintptr(fdcwd), uintptr(unsafe.Pointer(path)), O_RDONLY|O_CLOEXEC, 0, 0, 0)
	if errno != 0 {
		return false
	}
	var head [apeHeadSize]byte
	filled := 0
	for filled < len(head) {
		count, _, errno := RawSyscall(SYS_READ, fd, uintptr(unsafe.Pointer(&head[filled])), uintptr(len(head)-filled))
		if errno != 0 || count == 0 {
			break
		}
		filled += int(count)
	}
	RawSyscall(SYS_CLOSE, fd, 0, 0)
	return filled == len(head) && isAPEHead(&head)
}
