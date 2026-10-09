// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"internal/platform"
	"internal/syslist"
	"maps"
	"slices"
	"testing"
)

// TestFileNamesMatchSyslist verifies that shouldbuild excludes a file by the
// same GOOS and GOARCH suffixes go/build does.
func TestFileNamesMatchSyslist(t *testing.T) {
	cases := []struct {
		name  string
		dist  []string
		known map[string]bool
	}{
		{"GOOS", fileOS, syslist.KnownOS},
		{"GOARCH", fileArch, syslist.KnownArch},
	}
	for _, tc := range cases {
		got := slices.Sorted(slices.Values(tc.dist))
		want := slices.Sorted(maps.Keys(tc.known))
		if !slices.Equal(got, want) {
			t.Errorf("%s: dist matches %v; internal/syslist knows %v", tc.name, got, want)
		}
	}
}

// TestMustLinkExternal verifies that the mustLinkExternal helper
// function matches internal/platform.MustLinkExternal.
func TestMustLinkExternal(t *testing.T) {
	for _, goos := range okgoos {
		for _, goarch := range okgoarch {
			for _, cgoEnabled := range []bool{true, false} {
				got := mustLinkExternal(goos, goarch, cgoEnabled)
				want := platform.MustLinkExternal(goos, goarch, cgoEnabled)
				if got != want {
					t.Errorf("mustLinkExternal(%q, %q, %v) = %v; want %v", goos, goarch, cgoEnabled, got, want)
				}
			}
		}
	}
}

func TestRequiredBootstrapVersion(t *testing.T) {
	testCases := map[string]string{
		"1.22": "1.20",
		"1.23": "1.20",
		"1.24": "1.22",
		"1.25": "1.22",
		"1.26": "1.24",
		"1.27": "1.24",
	}

	for v, want := range testCases {
		if got := requiredBootstrapVersion(v); got != want {
			t.Errorf("requiredBootstrapVersion(%v): got %v, want %v", v, got, want)
		}
	}
}
