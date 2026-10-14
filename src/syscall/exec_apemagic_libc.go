// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build darwin || openbsd

package syscall

import (
	"internal/abi"
	"unsafe"
)

// hasAPEMagic reports whether the file at path starts with an APE header.
// The forked child calls it, so it must not allocate.
//
//go:nosplit
//go:norace
func hasAPEMagic(path *byte) bool {
	fd, _, errno := rawSyscall(abi.FuncPCABI0(libc_open_trampoline), uintptr(unsafe.Pointer(path)), O_RDONLY|O_CLOEXEC, 0)
	if errno != 0 {
		return false
	}
	var head [apeHeadSize]byte
	filled := 0
	for filled < len(head) {
		count, _, errno := rawSyscall(abi.FuncPCABI0(libc_read_trampoline), fd, uintptr(unsafe.Pointer(&head[filled])), uintptr(len(head)-filled))
		if errno != 0 || count == 0 {
			break
		}
		filled += int(count)
	}
	rawSyscall(abi.FuncPCABI0(libc_close_trampoline), fd, 0, 0)
	return filled == len(head) && isAPEHead(&head)
}
