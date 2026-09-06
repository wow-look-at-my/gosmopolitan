// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"testing"
)

// The two systems agree on no lock type at all, so a wrong mapping asks
// for a read lock where the caller wanted a write lock. That is a
// silently weaker lock rather than an error, which is why this runs on
// every host rather than only on a macOS one.
func TestDarwinLockType(t *testing.T) {
	for _, tc := range []struct {
		linux, apple int16
	}{
		{F_RDLCK, cosmo.DarwinF_RDLCK},
		{F_WRLCK, cosmo.DarwinF_WRLCK},
		{F_UNLCK, cosmo.DarwinF_UNLCK},
	} {
		got, ok := darwinLockType(tc.linux)
		if !ok || got != tc.apple {
			t.Errorf("darwinLockType(%d) = %d, %v; want %d, true", tc.linux, got, ok, tc.apple)
		}
		back, ok := linuxLockType(tc.apple)
		if !ok || back != tc.linux {
			t.Errorf("linuxLockType(%d) = %d, %v; want %d, true", tc.apple, back, ok, tc.linux)
		}
	}
	if _, ok := darwinLockType(9); ok {
		t.Error("lock type 9 accepted; Apple has no value for it")
	}
	if _, ok := linuxLockType(0); ok {
		t.Error("Apple lock type 0 accepted; Apple numbers its types from one")
	}
}
