// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo_test

import (
	"internal/runtime/syscall/cosmo"
	"testing"
)

// Apple's LIBC struct sigaction, what the Syslib's sigaction reads. It
// shares no field offset with the 32-byte Linux struct beyond the
// handler, which is why the arguments cannot be forwarded. Layout from
// runtime.xnuSigactiont (signal_cosmo_xnu.go).
func TestXnuSigactionLayout(t *testing.T) {
	size, handler, mask, flags := cosmo.XnuSigactionLayout()
	for _, f := range []struct {
		name      string
		got, want uintptr
	}{
		{"size", size, 16},
		{"sa_handler", handler, 0},
		{"sa_mask", mask, 8},
		{"sa_flags", flags, 12},
	} {
		if f.got != f.want {
			t.Errorf("xnuSigactiont %s = %d, want %d", f.name, f.got, f.want)
		}
	}
}
