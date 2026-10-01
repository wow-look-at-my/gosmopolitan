// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package linux_test

import (
	"internal/runtime/syscall/linux"
	"runtime"
	"testing"
)

func TestEpollctlErrorSign(t *testing.T) {
	// EpollCtl issues the syscall instruction itself, so it needs a Linux
	// kernel under it. The cosmo port builds this package for a binary
	// that runs on three hosts, and runtime.GOOS names the one it got.
	if runtime.GOOS != "linux" {
		t.Skipf("epoll is a Linux kernel interface; this host is %s", runtime.GOOS)
	}
	v := linux.EpollCtl(-1, 1, -1, &linux.EpollEvent{})

	const EBADF = 0x09
	if v != EBADF {
		t.Errorf("epollctl = %v, want %v", v, EBADF)
	}
}
