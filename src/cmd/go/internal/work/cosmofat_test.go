// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"reflect"
	"testing"

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
		{"compact", "compact"},
		{"0", ""},
		{"off", ""},
		{"1", ""},
		{"on", ""},
		{"Full", ""},
		{"SLIM", ""},
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
		// GOCOSMOSTRIP=0 suppresses all strip/sidecar flags; the debug
		// mode has nothing to apply to.
		{debug: "slim", strip: "0", want: base},
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
