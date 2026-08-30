// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "testing"

// The fork's consumers extract a gzipped tar on every host, so a windows
// distribution that only writes the .zip publishes nothing they can install.
func TestBinaryDistNames(t *testing.T) {
	for _, tt := range []struct {
		goos string
		base string
		tgz  string
		zip  string
	}{
		{"linux", "go1.27.0cosmo.linux-amd64", "go1.27.0cosmo.linux-amd64.tar.gz", ""},
		{"darwin", "go1.27.0cosmo.darwin-arm64", "go1.27.0cosmo.darwin-arm64.tar.gz", ""},
		{"windows", "go1.27.0cosmo.windows-amd64", "go1.27.0cosmo.windows-amd64.tar.gz", "go1.27.0cosmo.windows-amd64.zip"},
	} {
		tgz, zip := binaryDistNames(tt.goos, tt.base)
		if tgz != tt.tgz {
			t.Errorf("binaryDistNames(%q, %q) tgz = %q, want %q", tt.goos, tt.base, tgz, tt.tgz)
		}
		if zip != tt.zip {
			t.Errorf("binaryDistNames(%q, %q) zip = %q, want %q", tt.goos, tt.base, zip, tt.zip)
		}
	}
}
