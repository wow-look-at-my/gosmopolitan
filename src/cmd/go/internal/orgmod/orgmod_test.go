// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package orgmod

import "testing"

func TestBranch(t *testing.T) {
	tests := []struct {
		name     string
		comments []string
		want     string
	}{
		{"none", nil, ""},
		{"indirect alone", []string{"// indirect"}, ""},
		{"named", []string{"// branch=v1"}, "v1"},
		{"named after indirect", []string{"// indirect; branch=v1"}, "v1"},
		{"spaced", []string{"//  branch=release-2  "}, "release-2"},
		{"legacy auto", []string{"// go-toolchain:auto-branch=v1"}, "v1"},
		{"legacy plain", []string{"// go-toolchain:branch=v1"}, "v1"},
		{"legacy bare names nothing", []string{"// go-toolchain:auto-branch"}, ""},
		{"empty name", []string{"// branch="}, ""},
		{"prose", []string{"// keep this one on the branch we cut"}, ""},
		{"second comment", []string{"// indirect", "// branch=v1"}, "v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Branch(tt.comments); got != tt.want {
				t.Errorf("Branch(%q) = %q, want %q", tt.comments, got, tt.want)
			}
		})
	}
}

func TestPlaceholder(t *testing.T) {
	tests := []struct{ path, want string }{
		{"github.com/wow-look-at-my/dep", "v0.0.0"},
		{"github.com/wow-look-at-my/dep/v2", "v2.0.0"},
		{"gopkg.in/dep.v3", "v3.0.0"},
	}
	for _, tt := range tests {
		if got := Placeholder(tt.path); got != tt.want {
			t.Errorf("Placeholder(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
