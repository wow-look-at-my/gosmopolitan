// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package filepathlite_test

import (
	"internal/filepathlite"
	"testing"
)

// The NT branch only runs on a Windows host, and one APE is tested wherever it
// was built. Passing the host in is what lets a linux host exercise it.
func TestNTIsAbs(t *testing.T) {
	for _, tt := range []struct {
		path string
		want bool
	}{
		{`C:\Users\runneradmin\.cache`, true},
		{`c:/Users`, true},
		{`C:`, false},
		{`C:foo`, false},
		{`\\host\share\dir`, true},
		{`\Users`, false},
		{`Users\x`, false},
		{``, false},
		{`/usr/bin`, false},
	} {
		if got := filepathlite.NTIsAbs(tt.path, true); got != tt.want {
			t.Errorf("NTIsAbs(%q, nt) = %v, want %v", tt.path, got, tt.want)
		}
	}
	// The same paths on a host that is not NT: only a leading slash is absolute.
	for _, tt := range []struct {
		path string
		want bool
	}{
		{`C:\Users`, false},
		{`/usr/bin`, true},
		{`usr/bin`, false},
	} {
		if got := filepathlite.NTIsAbs(tt.path, false); got != tt.want {
			t.Errorf("NTIsAbs(%q, unix) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestNTVolumeNameLen(t *testing.T) {
	for _, tt := range []struct {
		path string
		want int
	}{
		{`C:\Users`, 2},
		{`C:`, 2},
		{`\\host\share\dir`, 12},
		{`\\host\share`, 12},
		{`//host/share/dir`, 12},
		{`\\host\`, 7},
		{`\\host`, 6},
		{`\Users`, 0},
		{`Users`, 0},
		{``, 0},
		// Device prefixes: the component after the prefix is part of the volume.
		{`\\.`, 3},
		{`\\?`, 3},
		{`\??`, 3},
		{`\\?\c:\dir`, 6},
		{`\\.\pipe\name`, 8},
		{`\??\c:\dir`, 6},
		{`\\?\UNC\host\share\dir`, 18},
		{`\\?\..\c:`, 0},
	} {
		if got := filepathlite.NTVolumeNameLen(tt.path, true); got != tt.want {
			t.Errorf("NTVolumeNameLen(%q, nt) = %d, want %d", tt.path, got, tt.want)
		}
		if got := filepathlite.NTVolumeNameLen(tt.path, false); got != 0 {
			t.Errorf("NTVolumeNameLen(%q, unix) = %d, want 0", tt.path, got)
		}
	}
}

func TestNTIsPathSeparator(t *testing.T) {
	if !filepathlite.NTIsPathSeparator('\\', true) {
		t.Error(`NTIsPathSeparator('\\', nt) = false, want true`)
	}
	if filepathlite.NTIsPathSeparator('\\', false) {
		t.Error(`NTIsPathSeparator('\\', unix) = true, want false`)
	}
	for _, nt := range []bool{true, false} {
		if !filepathlite.NTIsPathSeparator('/', nt) {
			t.Errorf("NTIsPathSeparator('/', %v) = false, want true", nt)
		}
	}
}
