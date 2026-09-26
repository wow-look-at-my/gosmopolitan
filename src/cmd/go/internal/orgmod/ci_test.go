// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package orgmod

import (
	"os"
	"testing"
)

func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestCIBuild(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		ancestor string
		want     bool
	}{
		{"CI with no agent", map[string]string{"GITHUB_ACTIONS": "true"}, "", true},
		{"not CI", map[string]string{}, "", false},
		{"CI spelled another way", map[string]string{"GITHUB_ACTIONS": "1"}, "", false},
		{"agent marker", map[string]string{"GITHUB_ACTIONS": "true", "CLAUDECODE": "1"}, "", false},
		{"another agent marker", map[string]string{"GITHUB_ACTIONS": "true", "GEMINI_CLI": "1"}, "", false},
		{"sandbox marker", map[string]string{"GITHUB_ACTIONS": "true", "CODEX_SANDBOX_NETWORK_DISABLED": "1"}, "", false},
		{"marker that says no agent", map[string]string{"GITHUB_ACTIONS": "true", "CLAUDECODE": "0"}, "", true},
		{"agent ancestor", map[string]string{"GITHUB_ACTIONS": "true"}, "claude", false},
		{"agent ancestor outside CI", map[string]string{}, "codex", false},
	} {
		anc := func() string { return tc.ancestor }
		if got := ciBuild(envOf(tc.env), anc); got != tc.want {
			t.Errorf("%s: ciBuild = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProcParentReadsThisProcess(t *testing.T) {
	comm, ppid, ok := procParent(os.Getpid())
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		// A host with no /proc must read as a host with no agent.
		if ok {
			t.Errorf("procParent(self) with no /proc = %q, %d; want no answer", comm, ppid)
		}
		return
	}
	if !ok || comm == "" || ppid != os.Getppid() {
		t.Errorf("procParent(self) = %q, %d; want a name and parent %d", comm, ppid, os.Getppid())
	}
}
