// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/syscall/windows"
	"unsafe"
)

// extraMWordSleep sleeps while *addr is 0. It may return early. It needs
// no m or g.
//
//go:nosplit
func extraMWordSleep(addr *uint32) {
	var zero uint32
	stdcall_no_g(_WaitOnAddress, uintptr(unsafe.Pointer(addr)), uintptr(unsafe.Pointer(&zero)), 4, windows.INFINITE)
}

// extraMWordWake wakes one thread sleeping on addr.
//
//go:nosplit
func extraMWordWake(addr *uint32) {
	stdcall_no_g(_WakeByAddressSingle, uintptr(unsafe.Pointer(addr)))
}
