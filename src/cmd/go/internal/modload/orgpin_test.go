// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modload

import (
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func noAncestor() string { return "" }

func TestOrgPinAllowed(t *testing.T) {
	ci := map[string]string{"GITHUB_ACTIONS": "true"}
	if err := orgPinAllowed(envOf(ci), noAncestor); err != nil {
		t.Fatalf("CI with no agent: %v", err)
	}

	for _, tc := range []struct {
		name     string
		env      map[string]string
		ancestor string
		want     string
	}{
		{"not CI", map[string]string{}, "", "honored only in CI"},
		{"CI spelled wrong", map[string]string{"GITHUB_ACTIONS": "1"}, "", "honored only in CI"},
		{"agent marker", map[string]string{"GITHUB_ACTIONS": "true", "CLAUDECODE": "1"}, "", "CLAUDECODE is set"},
		{"other agent marker", map[string]string{"GITHUB_ACTIONS": "true", "GEMINI_CLI": "1"}, "", "GEMINI_CLI is set"},
		{"agent ancestor", map[string]string{"GITHUB_ACTIONS": "true"}, "claude", "ancestor process claude"},
	} {
		anc := func() string { return tc.ancestor }
		err := orgPinAllowed(envOf(tc.env), anc)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", tc.name, err, tc.want)
		}
	}

	// A marker set to "0" says the agent is absent.
	off := map[string]string{"GITHUB_ACTIONS": "true", "CLAUDECODE": "0"}
	if err := orgPinAllowed(envOf(off), noAncestor); err != nil {
		t.Errorf("CLAUDECODE=0: %v", err)
	}
}

func TestParseOrgPins(t *testing.T) {
	pins, err := parseOrgPins(" github.com/wow-look-at-my/alpha=v0.0.0-20260102030405-0f69f837cebe\n github.com/wow-look-at-my/beta=v0.0.0-20260202030405-6dedeb498b02 ")
	if err != nil {
		t.Fatal(err)
	}
	if got := pins["github.com/wow-look-at-my/alpha"]; got != "v0.0.0-20260102030405-0f69f837cebe" {
		t.Errorf("alpha = %q", got)
	}
	if len(pins) != 2 {
		t.Errorf("got %d pins, want two", len(pins))
	}

	for in, want := range map[string]string{
		"github.com/wow-look-at-my/alpha":        "is not path=version",
		"example.com/other=v1.0.0":               "is not an org module",
		"github.com/wow-look-at-my/alpha=latest": "github.com/wow-look-at-my/alpha",
		"github.com/wow-look-at-my/alpha=v0.0.0-20260102030405-0f69f837cebe github.com/wow-look-at-my/alpha=v0.0.0-20260202030405-6dedeb498b02": "pinned to both",
	} {
		if _, err := parseOrgPins(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseOrgPins(%q) = %v, want an error containing %q", in, err, want)
		}
	}
}

func TestProcParentReadsThisProcess(t *testing.T) {
	comm, ppid, ok := procParent(1)
	if !ok {
		t.Skip("no /proc on this host")
	}
	if comm == "" || ppid < 0 {
		t.Errorf("procParent(1) = %q, %d", comm, ppid)
	}
}
