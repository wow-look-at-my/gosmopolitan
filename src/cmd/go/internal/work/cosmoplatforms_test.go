// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"reflect"
	"testing"

	"cmd/go/internal/cfg"
	"cmd/go/internal/load"
)

func TestCosmoPlatformSpec(t *testing.T) {
	tests := []struct {
		env          string
		want         string
		wantExplicit bool
	}{
		// Unset is the three platforms something verifies, not every
		// platform the table can name.
		{"", "linux/amd64,darwin/arm64,windows/amd64", false},
		{"linux/amd64", "linux/amd64", true},
		// The two the default leaves out stay selectable by name.
		{"darwin/amd64,linux/arm64", "linux/arm64,darwin/amd64", true},
		// Canonical order and deduplication, so the string handed to the
		// linker as -apeplatforms is the same for any spelling of a set.
		{"windows/amd64,linux/amd64,windows/amd64", "linux/amd64,windows/amd64", true},
	}
	for _, tt := range tests {
		t.Setenv(CosmoPlatformsEnv, tt.env)
		set, explicit := cosmoPlatformSpec()
		if set.String() != tt.want || explicit != tt.wantExplicit {
			t.Errorf("%s=%q: got %q explicit=%v, want %q explicit=%v",
				CosmoPlatformsEnv, tt.env, set, explicit, tt.want, tt.wantExplicit)
		}
		if got := CosmoPlatforms(); got != tt.want {
			t.Errorf("%s=%q: CosmoPlatforms() = %q, want %q", CosmoPlatformsEnv, tt.env, got, tt.want)
		}
	}
}

// TestCosmoSiblingAndAssemble covers which builds start a
// sibling-architecture build and which run the linker's APE assembly step.
// The pairing matters: a selection that needs one architecture must skip
// the sibling build (that is the slimming) and still assemble (so the
// output is stripped and gets its sidecar, like the fat build it replaces).
func TestCosmoSiblingAndAssemble(t *testing.T) {
	restore := func(goos, goarch string) func() {
		return func() { cfg.Goos, cfg.Goarch = goos, goarch }
	}
	t.Cleanup(restore(cfg.Goos, cfg.Goarch))

	tests := []struct {
		goos, goarch string
		fat, inner   string
		platforms    string
		wantSibling  string
		wantAssemble bool
	}{
		{goos: "cosmo", goarch: "amd64", wantSibling: "arm64", wantAssemble: true},
		{goos: "cosmo", goarch: "arm64", wantSibling: "amd64", wantAssemble: true},
		{goos: "linux", goarch: "amd64", wantSibling: "", wantAssemble: false},
		// The sibling build itself must produce a plain single-architecture
		// binary: it is an input to the assembly, not a subject of it.
		{goos: "cosmo", goarch: "arm64", inner: "1", wantSibling: "", wantAssemble: false},
		// GOCOSMOFAT=0 keeps today's meaning: no sibling, and no assembly
		// either, so a thin build is neither stripped nor given sidecars.
		{goos: "cosmo", goarch: "amd64", fat: "0", wantSibling: "", wantAssemble: false},
		// A selection spanning both payloads behaves like a fat build.
		{goos: "cosmo", goarch: "amd64", platforms: "linux/amd64,darwin/arm64", wantSibling: "arm64", wantAssemble: true},
		// A selection all of whose platforms boot one payload drops the
		// sibling build and keeps the assembly step.
		{goos: "cosmo", goarch: "amd64", platforms: "linux/amd64,windows/amd64", wantSibling: "", wantAssemble: true},
		{goos: "cosmo", goarch: "arm64", platforms: "darwin/arm64", wantSibling: "", wantAssemble: true},
	}
	for _, tt := range tests {
		cfg.Goos, cfg.Goarch = tt.goos, tt.goarch
		t.Setenv("GOCOSMOFAT", tt.fat)
		t.Setenv("GOCOSMOFAT_INNER", tt.inner)
		t.Setenv(CosmoPlatformsEnv, tt.platforms)

		if got := cosmoSiblingArch(); got != tt.wantSibling {
			t.Errorf("GOOS=%s GOARCH=%s GOCOSMOFAT=%q inner=%q %s=%q: cosmoSiblingArch() = %q, want %q",
				tt.goos, tt.goarch, tt.fat, tt.inner, CosmoPlatformsEnv, tt.platforms, got, tt.wantSibling)
		}
		if got := cosmoFatEnabled(); got != (tt.wantSibling != "") {
			t.Errorf("GOOS=%s GOARCH=%s %s=%q: cosmoFatEnabled() = %v, want %v",
				tt.goos, tt.goarch, CosmoPlatformsEnv, tt.platforms, got, tt.wantSibling != "")
		}
		if got := cosmoAssembleEnabled(); got != tt.wantAssemble {
			t.Errorf("GOOS=%s GOARCH=%s GOCOSMOFAT=%q inner=%q %s=%q: cosmoAssembleEnabled() = %v, want %v",
				tt.goos, tt.goarch, tt.fat, tt.inner, CosmoPlatformsEnv, tt.platforms, got, tt.wantAssemble)
		}
	}
}

// TestCosmoMergeArgsPlatforms covers what the assembly step is told: the
// selection rides along as -apeplatforms, and a build with no sibling
// passes one input rather than a dangling comma.
func TestCosmoMergeArgsPlatforms(t *testing.T) {
	p := &load.Package{}
	p.Target = "app.com"

	tests := []struct {
		platforms string
		sibling   string
		want      []string
	}{
		{
			sibling: "sib.com",
			want:    []string{"-apefat", "app.com,sib.com", "-o", "app.com", "-apestrip", "-apedbg"},
		},
		{
			platforms: "linux/amd64,darwin/arm64",
			sibling:   "sib.com",
			want: []string{"-apefat", "app.com,sib.com", "-o", "app.com",
				"-apeplatforms=linux/amd64,darwin/arm64", "-apestrip", "-apedbg"},
		},
		{
			platforms: "windows/amd64,linux/amd64",
			want: []string{"-apefat", "app.com", "-o", "app.com",
				"-apeplatforms=linux/amd64,windows/amd64", "-apestrip", "-apedbg"},
		},
	}
	for _, tt := range tests {
		t.Setenv(CosmoPlatformsEnv, tt.platforms)
		t.Setenv("GOCOSMOSTRIP", "")
		t.Setenv("GOCOSMODEBUG", "")
		if got := cosmoMergeArgs(p, tt.sibling); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s=%q sibling=%q: cosmoMergeArgs = %q, want %q",
				CosmoPlatformsEnv, tt.platforms, tt.sibling, got, tt.want)
		}
	}
}

// TestCosmoEnvReportsEffectiveValues covers the values go env reports. They
// must be what the build acts on: a consumer that reads GOCOSMOFAT=on and
// gets a single-architecture binary has been told something false.
func TestCosmoEnvReportsEffectiveValues(t *testing.T) {
	tests := []struct {
		fat, strip, debug, platforms  string
		wantFat, wantStrip, wantDebug string
	}{
		{wantFat: "on", wantStrip: "on", wantDebug: "full"},
		{fat: "0", wantFat: "off", wantStrip: "on", wantDebug: "full"},
		{strip: "off", wantFat: "on", wantStrip: "off", wantDebug: "full"},
		{debug: "min", wantFat: "on", wantStrip: "on", wantDebug: "min"},
		// One architecture: not a fat build, whatever GOCOSMOFAT says.
		{platforms: "linux/amd64,windows/amd64", wantFat: "off", wantStrip: "on", wantDebug: "full"},
		{platforms: "linux/amd64,darwin/arm64", wantFat: "on", wantStrip: "on", wantDebug: "full"},
	}
	for _, tt := range tests {
		t.Setenv("GOCOSMOFAT", tt.fat)
		t.Setenv("GOCOSMOSTRIP", tt.strip)
		t.Setenv("GOCOSMODEBUG", tt.debug)
		t.Setenv(CosmoPlatformsEnv, tt.platforms)
		if got := CosmoFat(); got != tt.wantFat {
			t.Errorf("GOCOSMOFAT=%q %s=%q: CosmoFat() = %q, want %q", tt.fat, CosmoPlatformsEnv, tt.platforms, got, tt.wantFat)
		}
		if got := CosmoStrip(); got != tt.wantStrip {
			t.Errorf("GOCOSMOSTRIP=%q: CosmoStrip() = %q, want %q", tt.strip, got, tt.wantStrip)
		}
		if got := CosmoDebug(); got != tt.wantDebug {
			t.Errorf("GOCOSMODEBUG=%q: CosmoDebug() = %q, want %q", tt.debug, got, tt.wantDebug)
		}
	}
}
