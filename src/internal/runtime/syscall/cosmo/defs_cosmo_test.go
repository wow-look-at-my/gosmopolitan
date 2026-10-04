// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package cosmo_test

import (
	"internal/runtime/syscall/cosmo"
	"runtime"
	"testing"
	"unsafe"
)

// TestEpollEventLayout verifies that EpollEvent matches the Linux
// kernel's struct epoll_event ABI for the current architecture. A
// mismatch makes the kernel write past the end of the events array in
// netpoll.
func TestEpollEventLayout(t *testing.T) {
	var ev cosmo.EpollEvent
	var wantSize, wantOff uintptr
	switch runtime.GOARCH {
	case "amd64":
		wantSize, wantOff = 12, 4
	case "arm64":
		wantSize, wantOff = 16, 8
	default:
		t.Skipf("no expected EpollEvent layout for GOARCH=%s", runtime.GOARCH)
	}
	if got := unsafe.Sizeof(ev); got != wantSize {
		t.Errorf("unsafe.Sizeof(EpollEvent) = %d, want %d", got, wantSize)
	}
	if got := unsafe.Offsetof(ev.Data); got != wantOff {
		t.Errorf("unsafe.Offsetof(EpollEvent.Data) = %d, want %d", got, wantOff)
	}
}
