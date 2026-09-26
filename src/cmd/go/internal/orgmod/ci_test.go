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
	for _, tcase := range []struct {
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
		ancestor := func() string { return tcase.ancestor }
		if got := ciBuild(envOf(tcase.env), ancestor); got != tcase.want {
			t.Errorf("%s: ciBuild = %v, want %v", tcase.name, got, tcase.want)
		}
	}
}

func TestProcParentReadsThisProcess(t *testing.T) {
	comm, ppid, found := procParent(os.Getpid())
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		// A host with no /proc must read as a host with no agent.
		if found {
			t.Errorf("procParent(self) with no /proc = %q, %d; want no answer", comm, ppid)
		}
		return
	}
	if !found || comm == ""|| ppid != os.Getppid() {
		t.Errorf("procParent(self) = %q, %d; want a name and parent %d", comm, ppid, os.Getppid())
	}
}
