// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"cmd/go/internal/cfg"
	"cmd/go/internal/load"
)

func TestLdflagsSpecifyStrip(t *testing.T) {
	tests := []struct {
		flags []string
		want  bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"-X", "main.version=1"}, false},
		{[]string{"-buildid="}, false},
		{[]string{"-extldflags=-static -s"}, false}, // -s inside a value, not a flag
		{[]string{"main.s=1"}, false},               // value token, not a flag
		{[]string{"-swallow"}, false},               // -s is not a prefix match
		{[]string{"-white"}, false},
		{[]string{"-s"}, true},
		{[]string{"-w"}, true},
		{[]string{"--s"}, true},
		{[]string{"--w"}, true},
		{[]string{"-s=true"}, true},
		{[]string{"-w=false"}, true}, // explicit user choice, either value
		{[]string{"-X", "a=b", "-w"}, true},
		{[]string{"-s", "-w"}, true},
	}
	for _, tt := range tests {
		if got := ldflagsSpecifyStrip(tt.flags); got != tt.want {
			t.Errorf("ldflagsSpecifyStrip(%q) = %v, want %v", tt.flags, got, tt.want)
		}
	}
}

func TestCosmoDebugMode(t *testing.T) {
	tests := []struct {
		v    string
		want string // "" means invalid
	}{
		{"", "full"},
		{"full", "full"},
		{"slim", "slim"},
		{"min", "min"},
		{"compact", "compact"},
		{"0", ""},
		{"off", ""},
		{"1", ""},
		{"on", ""},
		{"Full", ""},
		{"SLIM", ""},
		{"MIN", ""},
		{"minimal", ""},
		{"pristine", ""},
		{"slim,compact", ""},
	}
	for _, tt := range tests {
		got, err := parseCosmoDebugMode(tt.v)
		if tt.want == "" {
			if err == nil {
				t.Errorf("parseCosmoDebugMode(%q) = %q, want error", tt.v, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseCosmoDebugMode(%q): unexpected error: %v", tt.v, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseCosmoDebugMode(%q) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

func TestCosmoMergeArgsDebugMode(t *testing.T) {
	p := &load.Package{}
	p.Target = "app.com"
	base := []string{"-apefat", "app.com,sib.com", "-o", "app.com"}
	strip := append(append([]string(nil), base...), "-apestrip", "-apedbg")

	tests := []struct {
		debug   string
		strip   string // GOCOSMOSTRIP
		ldflags []string
		want    []string
	}{
		{debug: "", want: strip},
		{debug: "full", want: strip},
		{debug: "slim", want: append(append([]string(nil), strip...), "-apedbgmode=slim")},
		{debug: "compact", want: append(append([]string(nil), strip...), "-apedbgmode=compact")},
		// min maps to the linker's slim transform: its extra
		// reduction happens at compile time, not merge time.
		{debug: "min", want: append(append([]string(nil), strip...), "-apedbgmode=slim")},
		// GOCOSMOSTRIP=0 suppresses all strip/sidecar flags; the debug
		// mode has nothing to apply to.
		{debug: "slim", strip: "0", want: base},
		{debug: "min", strip: "0", want: base},
		// An explicit user -s/-w likewise.
		{debug: "compact", ldflags: []string{"-s"}, want: base},
	}
	for _, tt := range tests {
		t.Setenv("GOCOSMODEBUG", tt.debug)
		if tt.strip != "" {
			t.Setenv("GOCOSMOSTRIP", tt.strip)
		} else {
			t.Setenv("GOCOSMOSTRIP", "")
		}
		p.Internal.Ldflags = tt.ldflags
		if got := cosmoMergeArgs(p, "sib.com"); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("GOCOSMODEBUG=%q GOCOSMOSTRIP=%q ldflags %q: cosmoMergeArgs = %q, want %q",
				tt.debug, tt.strip, tt.ldflags, got, tt.want)
		}
	}
}

// TestCosmoDebugModeMinGcflags covers the compile-time flag injection of
// the min mode: which modes inject, what they inject, and the
// cosmoBuildInit gating on GOOS and toolchain.
func TestCosmoDebugModeMinGcflags(t *testing.T) {
	for _, tt := range []struct {
		mode string
		want []string
	}{
		{"full", nil},
		{"slim", nil},
		{"compact", nil},
		{"min", []string{"-dwarflocationlists=false", "-gendwarfinl=0"}},
	} {
		if got := cosmoDebugGcflags(tt.mode); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("cosmoDebugGcflags(%q) = %q, want %q", tt.mode, got, tt.want)
		}
	}

	t.Serial("cfg and forcedGcflags are package globals that every build in this process reads")
	restore := func(goos, toolchain string, forced []string) func() {
		return func() {
			cfg.Goos = goos
			cfg.BuildToolchainName = toolchain
			forcedGcflags = forced
		}
	}
	t.Cleanup(restore(cfg.Goos, cfg.BuildToolchainName, forcedGcflags))

	for _, tt := range []struct {
		goos      string
		toolchain string
		mode      string
		want      []string
	}{
		{"cosmo", "gc", "min", []string{"-dwarflocationlists=false", "-gendwarfinl=0"}},
		{"cosmo", "gc", "slim", nil},
		{"cosmo", "gc", "", nil},
		{"linux", "gc", "min", nil},    // min is a cosmo knob
		{"cosmo", "gccgo", "min", nil}, // gc-only flags
	} {
		cfg.Goos = tt.goos
		cfg.BuildToolchainName = tt.toolchain
		forcedGcflags = nil
		t.Setenv("GOCOSMODEBUG", tt.mode)
		cosmoBuildInit()
		if !reflect.DeepEqual(forcedGcflags, tt.want) {
			t.Errorf("GOOS=%s toolchain=%s GOCOSMODEBUG=%q: forcedGcflags = %q, want %q",
				tt.goos, tt.toolchain, tt.mode, forcedGcflags, tt.want)
		}
	}
}

func TestCosmoStripEnabled(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"", true},
		{"1", true},
		{"on", true},
		{"anything", true},
		{"0", false},
		{"off", false},
	}
	for _, tt := range tests {
		t.Setenv("GOCOSMOSTRIP", tt.env)
		if got := cosmoStripEnabled(); got != tt.want {
			t.Errorf("GOCOSMOSTRIP=%q: cosmoStripEnabled() = %v, want %v", tt.env, got, tt.want)
		}
	}
}

func TestCosmoFatParallel(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"", true},
		{"0", true},
		{"off", true},
		{"anything", true},
		{"1", false},
		{"on", false},
	}
	for _, tt := range tests {
		t.Setenv("GOCOSMOFATSEQ", tt.env)
		if got := cosmoFatParallel(); got != tt.want {
			t.Errorf("GOCOSMOFATSEQ=%q: cosmoFatParallel() = %v, want %v", tt.env, got, tt.want)
		}
	}
}

func TestCosmoFatSkipOutput(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "prog.com")
	if err := os.WriteFile(regular, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		out  string
		want bool
	}{
		// No -o: the target is a package default name, always regular.
		{"unset", "", false},
		// A regular file is the ordinary fat-build target.
		{"regular file", regular, false},
		// A -o directory is the dir=true fat build, not a skip.
		{"directory", dir, false},
		// Not yet created: go build is about to write a regular file.
		{"nonexistent", filepath.Join(dir, "notyet.com"), false},
		// The case that must skip: fattening re-reads and re-creates the
		// target, which cannot work for a special file, so a sibling
		// build would be wasted work.
		{"/dev/null", os.DevNull, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.out == os.DevNull {
				if fi, err := os.Stat(os.DevNull); err != nil || fi.Mode().IsRegular() {
					t.Skip("no special-file os.DevNull on this platform")
				}
			}
			t.Serial("cfg.BuildO is a package global, and the output name it holds decides where a sibling build writes")
			defer func(old string) { cfg.BuildO = old }(cfg.BuildO)
			cfg.BuildO = tt.out
			if got := cosmoFatSkipOutput(); got != tt.want {
				t.Errorf("cfg.BuildO=%q: cosmoFatSkipOutput() = %v, want %v", tt.out, got, tt.want)
			}
		})
	}
}
