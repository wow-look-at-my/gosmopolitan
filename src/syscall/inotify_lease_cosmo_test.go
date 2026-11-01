// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "testing"

func TestKernelLeaseWaits(t *testing.T) {
	cases := []struct {
		name      string
		errno     Errno
		unchanged bool
		want      bool
	}{
		{"a writer holds the file", EAGAIN, false, true},
		{"a writer holds a file of the same size", EAGAIN, true, true},
		{"another user's file grew", EACCES, false, true},
		{"another user's file kept its size", EACCES, true, false},
		{"the file left", ENOENT, true, true},
	}
	for _, test := range cases {
		if got := kernelLeaseWaits(test.errno, test.unchanged); got != test.want {
			t.Errorf("%s: kernelLeaseWaits(%v, %v) = %v, want %v", test.name, test.errno, test.unchanged, got, test.want)
		}
	}
}
