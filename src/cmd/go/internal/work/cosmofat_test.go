// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import "testing"

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
