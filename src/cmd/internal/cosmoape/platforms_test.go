// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cosmoape

import (
	"reflect"
	"strings"
	"testing"
)

// TestPlatformTableIsClosed pins the whole platform table, and
// windows/arm64's absence in particular.
//
// os_cosmo_nt_arm64.go answers every entry point with a throw, which is
// safe only because no APE this toolchain emits starts on that host.
// Adding the row without the runtime turns those throws into a crash in
// the scheduler. If this test stopped you, that is the work it asks for.
func TestPlatformTableIsClosed(t *testing.T) {
	want := []Platform{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
		{"windows", "amd64"},
	}
	if !reflect.DeepEqual(all[:], want) {
		t.Fatalf("platform table = %v, want %v", all, want)
	}
	for _, p := range Default().Platforms() {
		if p.OS == "windows" && p.Arch != "amd64" {
			t.Errorf("%s is bootable, but runtime has no NT support on %s", p, p.Arch)
		}
	}
}

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

// TestDefaultIsTheSupportedThree pins what a build with no
// GOCOSMOPLATFORMS claims. The default is narrower than the table on
// purpose: linux/arm64 and darwin/amd64 are selectable but not promised,
// and darwin/amd64 in particular has never executed (no Intel-mac runner),
// so a default build must not advertise it.
//
// Both arches are still required, because darwin/arm64 is in the set.
// Narrowing the default is an accuracy change, not a size one.
func TestDefaultIsTheSupportedThree(t *testing.T) {
	d := Default()
	want := []Platform{LinuxAMD64, DarwinARM64, WindowsAMD64}
	if got := d.Platforms(); !reflect.DeepEqual(got, want) {
		t.Errorf("Default() = %v, want %v", got, want)
	}
	for _, p := range []Platform{LinuxARM64, DarwinAMD64} {
		if d.Has(p) {
			t.Errorf("Default() claims %s, which nothing verifies", p)
		}
		if _, err := Parse(p.String()); err != nil {
			t.Errorf("Parse(%q) = %v, want it to stay selectable", p, err)
		}
	}
	if got := d.Arches(); !reflect.DeepEqual(got, []string{"amd64", "arm64"}) {
		t.Errorf("Default().Arches() = %v, want [amd64 arm64]", got)
	}
}

func TestRestrictToArches(t *testing.T) {
	// A build with no explicit selection supports what its payloads allow:
	// an amd64-only build claims no arm64 platform. darwin/amd64 is absent
	// because the default never contained it, not because of the arch
	// filter.
	got := Default().RestrictToArches([]string{"amd64"})
	want := []Platform{LinuxAMD64, WindowsAMD64}
	if !reflect.DeepEqual(got.Platforms(), want) {
		t.Errorf("Default().RestrictToArches([amd64]) = %v, want %v", got.Platforms(), want)
	}
	if got.NeedsArch("arm64") {
		t.Error("amd64-only set still needs an arm64 payload")
	}
	// The arm64 half of the same build claims darwin and nothing else.
	arm := Default().RestrictToArches([]string{"arm64"})
	if w := []Platform{DarwinARM64}; !reflect.DeepEqual(arm.Platforms(), w) {
		t.Errorf("Default().RestrictToArches([arm64]) = %v, want %v", arm.Platforms(), w)
	}
}
