// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package os_test

import (
	"os"
	"slices"
	"testing"
)

// An NT PATH parts on the semicolon. A colon there cuts every entry after
// its drive letter, which sends the executable search to directories that do
// not exist.
func TestSplitPathListNT(tst *testing.T) {
	got := os.SplitPathListSep(`C:\Windows;C:\Program Files\Go\bin`, ';')
	want := []string{`C:\Windows`, `C:\Program Files\Go\bin`}
	if !slices.Equal(got, want) {
		tst.Errorf("SplitPathListSep(NT) = %q, want %q", got, want)
	}
}

func TestSplitPathListUnix(tst *testing.T) {
	for _, tt := range []struct {
		list string
		want []string
	}{
		{"/usr/bin:/bin", []string{"/usr/bin", "/bin"}},
		{":/bin", []string{"", "/bin"}},
		{"", nil},
	} {
		if got := os.SplitPathListSep(tt.list, ':'); !slices.Equal(got, tt.want) {
			tst.Errorf("SplitPathListSep(%q) = %q, want %q", tt.list, got, tt.want)
		}
	}
}
