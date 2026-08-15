// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cosmoape

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		spec   string
		want   []Platform
		arches []string
	}{
		{"linux/amd64", []Platform{LinuxAMD64}, []string{"amd64"}},
		{"linux/amd64,windows/amd64", []Platform{LinuxAMD64, WindowsAMD64}, []string{"amd64"}},
		{"darwin/arm64", []Platform{DarwinARM64}, []string{"arm64"}},
		{"windows/amd64,darwin/arm64,linux/amd64", []Platform{LinuxAMD64, DarwinARM64, WindowsAMD64}, []string{"amd64", "arm64"}},
		{"linux/amd64,linux/amd64", []Platform{LinuxAMD64}, []string{"amd64"}},
		{" linux/amd64 , darwin/arm64 ", []Platform{LinuxAMD64, DarwinARM64}, []string{"amd64", "arm64"}},
	}
	for _, tt := range tests {
		s, err := Parse(tt.spec)
		if err != nil {
			t.Errorf("Parse(%q): %v", tt.spec, err)
			continue
		}
		if got := s.Platforms(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Parse(%q).Platforms() = %v, want %v", tt.spec, got, tt.want)
		}
		if got := s.Arches(); !reflect.DeepEqual(got, tt.arches) {
			t.Errorf("Parse(%q).Arches() = %v, want %v", tt.spec, got, tt.arches)
		}
		// The canonical spelling must round-trip, since cmd/go hands it
		// to cmd/link as -apeplatforms.
		back, err := Parse(s.String())
		if err != nil || back != s {
			t.Errorf("Parse(%q).String() = %q, which reparses to %v (err %v)", tt.spec, s.String(), back, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	// Every rejected spelling must name the offending token, so a typo is
	// fixable from the error alone.
	tests := []struct {
		spec string
		want string
	}{
		{"", `empty platform`},
		{",", `empty platform`},
		{"linux/amd64,", `empty platform`},
		{"linux/386", `unknown platform "linux/386"`},
		{"windows/arm64", `unknown platform "windows/arm64"`},
		{"linux", `unknown platform "linux"`},
		{"all", `unknown platform "all"`},
	}
	for _, tt := range tests {
		s, err := Parse(tt.spec)
		if err == nil {
			t.Errorf("Parse(%q) = %v, want error", tt.spec, s)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%q) error = %q, want it to contain %q", tt.spec, err, tt.want)
		}
		if !strings.Contains(err.Error(), "linux/amd64") {
			t.Errorf("Parse(%q) error = %q, want it to list the accepted platforms", tt.spec, err)
		}
	}
}

func TestDefaultCoversEveryPlatform(t *testing.T) {
	d := Default()
	for _, p := range all {
		if !d.Has(p) {
			t.Errorf("Default() lacks %s", p)
		}
	}
	if got := d.Arches(); !reflect.DeepEqual(got, []string{"amd64", "arm64"}) {
		t.Errorf("Default().Arches() = %v, want [amd64 arm64]", got)
	}
}

func TestRestrictToArches(t *testing.T) {
	// A build with no explicit selection supports what its payloads allow:
	// an amd64-only build claims no arm64 platform.
	got := Default().RestrictToArches([]string{"amd64"})
	want := []Platform{LinuxAMD64, DarwinAMD64, WindowsAMD64}
	if !reflect.DeepEqual(got.Platforms(), want) {
		t.Errorf("Default().RestrictToArches([amd64]) = %v, want %v", got.Platforms(), want)
	}
	if got.NeedsArch("arm64") {
		t.Error("amd64-only set still needs an arm64 payload")
	}
}
