// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"syscall"
	"testing"
)

func TestKernelLeaseWaits(t *testing.T) {
	cases := []struct {
		name      string
		errno     syscall.Errno
		unchanged bool
		want      bool
	}{
		{"a writer holds the file", syscall.EAGAIN, false, true},
		{"a writer holds a file of the same size", syscall.EAGAIN, true, true},
		{"another user's file grew", syscall.EACCES, false, true},
		{"another user's file kept its size", syscall.EACCES, true, false},
		{"the file left", syscall.ENOENT, true, true},
	}
	for _, test := range cases {
		if got := syscall.KernelLeaseWaitsForTest(test.errno, test.unchanged); got != test.want {
			t.Errorf("%s: kernelLeaseWaits(%v, %v) = %v, want %v", test.name, test.errno, test.unchanged, got, test.want)
		}
	}
}
