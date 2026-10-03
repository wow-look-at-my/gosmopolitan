// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package exec

import (
	"slices"
	"testing"
)

// The nt* path-syntax helpers are pure functions with no NT
// dependency, so they are testable on any host (this file runs under
// GOOS=cosmo via the misc/cosmo exec wrappers). The end-to-end NT
// behavior is CI-gated by testdata/runtimeprobe's lookpath check on
// the windows leg.

func TestNTSplitList(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{``, nil},
		{`C:\a`, []string{`C:\a`}},
		{`C:\a;D:\b`, []string{`C:\a`, `D:\b`}},
		{`C:\hostedtoolcache\windows\go\1.25.5\x64\bin;C:\Windows\system32`,
			[]string{`C:\hostedtoolcache\windows\go\1.25.5\x64\bin`, `C:\Windows\system32`}},
		// Quotes protect ';' and are stripped, per windows
		// filepath.SplitList.
		{`"C:\a;b";D:\c`, []string{`C:\a;b`, `D:\c`}},
		{`C:\a;;D:\b`, []string{`C:\a`, ``, `D:\b`}}, // empty entries preserved (skipped by lookup)
		{`/tmp/x;C:\a`, []string{`/tmp/x`, `C:\a`}},  // mixed-form PATH
	} {
		if got := ntSplitList(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("ntSplitList(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNTJoin(t *testing.T) {
	for _, tt := range []struct {
		dir, file, want string
	}{
		{``, `go`, `go`},
		{`C:\bin`, `go`, `C:\bin\go`},
		{`C:\bin\`, `go`, `C:\bin\go`},
		{`C:/bin`, `go`, `C:/bin\go`}, // drive-shaped: NT separator
		{`C:`, `go`, `C:go`},          // drive-relative, windows Join semantics
		{`C:\`, `go`, `C:\go`},
		{`/c/bin`, `go`, `/c/bin/go`},
		{`/tmp`, `f.exe`, `/tmp/f.exe`},
		{`relative`, `go`, `relative/go`},
		{`mixed\dir`, `go`, `mixed\dir\go`},
	} {
		if got := ntJoin(tt.dir, tt.file); got != tt.want {
			t.Errorf("ntJoin(%q, %q) = %q, want %q", tt.dir, tt.file, got, tt.want)
		}
	}
}

func TestNTIsAbs(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{`/c/bin/go`, true},
		{`/tmp/x`, true},
		{`C:\bin\go.exe`, true},
		{`c:/bin/go`, true},
		{`C:foo`, false}, // drive-relative
		{`\\srv\share\x`, true},
		{`go`, false},
		{`./go`, false},
		{`sub\go`, false},
		{``, false},
	} {
		if got := ntIsAbs(tt.in); got != tt.want {
			t.Errorf("ntIsAbs(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestNTExtHasExt(t *testing.T) {
	for _, tt := range []struct {
		in     string
		ext    string
		hasExt bool
	}{
		{`go.exe`, `.exe`, true},
		{`go`, ``, false},
		{`C:\bin\go.exe`, `.exe`, true},
		{`C:\bin.d\go`, ``, false}, // dot in a parent element only
		{`./prog`, ``, false},
		{`a.b\c`, ``, false},
		{`a.b/c.exe`, `.exe`, true},
	} {
		if got := ntExt(tt.in); got != tt.ext {
			t.Errorf("ntExt(%q) = %q, want %q", tt.in, got, tt.ext)
		}
		if got := ntHasExt(tt.in); got != tt.hasExt {
			t.Errorf("ntHasExt(%q) = %v, want %v", tt.in, got, tt.hasExt)
		}
	}
}

func TestNTLookupEnvFold(t *testing.T) {
	// Case-insensitive name match: the canonical NT block spelling
	// "Path" must be found by a "PATH" lookup (cosmo's os.Getenv is
	// exact-case; the fold is os/exec's own).
	t.Setenv("NtLpProbeVar", "folded")
	if got := ntGetenv("NTLPPROBEVAR"); got != "folded" {
		t.Errorf(`ntGetenv("NTLPPROBEVAR") = %q, want "folded"`, got)
	}
	if got := ntGetenv("ntlpprobevar"); got != "folded" {
		t.Errorf(`ntGetenv("ntlpprobevar") = %q, want "folded"`, got)
	}
	if _, found := ntLookupEnv("NtLpProbeVarAbsent"); found {
		t.Error("ntLookupEnv found an absent variable")
	}
}

func TestNTPathExt(t *testing.T) {
	t.Setenv("PATHEXT", ".EXE;.COM;BAT")
	if got, want := ntPathExt(), []string{".exe", ".com", ".bat"}; !slices.Equal(got, want) {
		t.Errorf("ntPathExt() = %q, want %q (lowercased, dot added)", got, want)
	}
	t.Setenv("PATHEXT", "")
	if got, want := ntPathExt(), []string{".com", ".exe", ".bat", ".cmd"}; !slices.Equal(got, want) {
		t.Errorf("ntPathExt() with empty PATHEXT = %q, want default %q", got, want)
	}
}
