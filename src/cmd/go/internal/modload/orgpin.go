// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modload

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"cmd/go/internal/orgmod"

	"golang.org/x/mod/module"
)

// GOORGPIN fixes the version of org modules for a whole CI run. The run
// resolves each branch head once and passes the answer to every job, so a
// commit that lands mid-run cannot reach only some of them.
//
// Only CI may pin. A pin anywhere else could hold an org module at an old
// commit to dodge a new one, so a set GOORGPIN outside CI is an error.
// See docs/ORG-DEPS.md.

// orgPins parses GOORGPIN once per process.
var orgPins = sync.OnceValues(func() (map[string]string, error) {
	s := os.Getenv("GOORGPIN")
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	if err := orgPinAllowed(os.Getenv, agentAncestor); err != nil {
		return nil, err
	}
	return parseOrgPins(s)
})

// orgPinAllowed reports why this process may not pin, or nil when it may.
func orgPinAllowed(getenv func(string) string, ancestor func() string) error {
	if getenv("GITHUB_ACTIONS") != "true" {
		return errors.New("GOORGPIN: honored only in CI (GITHUB_ACTIONS=true)")
	}
	for _, v := range agentEnvMarkers {
		if val := getenv(v); val != "" && val != "0" {
			return fmt.Errorf("GOORGPIN: refused under a coding agent (%s is set)", v)
		}
	}
	if name := ancestor(); name != "" {
		return fmt.Errorf("GOORGPIN: refused under a coding agent (ancestor process %s)", name)
	}
	return nil
}

// agentEnvMarkers and agentProcPrefixes mirror the roster of
// github.com/wow-look-at-my/is-this-an-agent. The toolchain vendors no new
// dependency, so the lists are copied here.
var (
	agentEnvMarkers   = []string{"CLAUDECODE", "GROK_AGENT", "CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "GEMINI_CLI", "OPENCODE"}
	agentProcPrefixes = []string{"claude", "grok", "xai-grok-pager", "codex", "gemini", "opencode"}
)

// agentAncestor returns the name of an ancestor process that is a coding
// agent, or "". It reads /proc, so it finds nothing where /proc is absent.
func agentAncestor() string {
	pid := os.Getppid()
	for depth := 0; pid > 1 && depth < 64; depth++ {
		comm, ppid, ok := procParent(pid)
		if !ok {
			return ""
		}
		for _, p := range agentProcPrefixes {
			if strings.HasPrefix(comm, p) {
				return comm
			}
		}
		pid = ppid
	}
	return ""
}

// procParent reads the command name and parent pid of pid from /proc.
func procParent(pid int) (comm string, ppid int, ok bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false
	}
	// The command name sits in parentheses and may hold spaces or ")".
	s := string(data)
	open := strings.IndexByte(s, '(')
	shut := strings.LastIndexByte(s, ')')
	if open < 0 || shut < open {
		return "", 0, false
	}
	fields := strings.Fields(s[shut+1:])
	if len(fields) < 2 {
		return "", 0, false
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, false
	}
	return s[open+1 : shut], ppid, true
}

// parseOrgPins reads whitespace-separated path=version entries.
func parseOrgPins(s string) (map[string]string, error) {
	pins := map[string]string{}
	for _, field := range strings.Fields(s) {
		path, version, ok := strings.Cut(field, "=")
		if !ok || path == "" || version == "" {
			return nil, fmt.Errorf("GOORGPIN: %q is not path=version", field)
		}
		if !orgmod.IsOrg(path) {
			return nil, fmt.Errorf("GOORGPIN: %s is not an org module", path)
		}
		if err := module.Check(path, version); err != nil {
			return nil, fmt.Errorf("GOORGPIN: %w", err)
		}
		if old, dup := pins[path]; dup && old != version {
			return nil, fmt.Errorf("GOORGPIN: %s is pinned to both %s and %s", path, old, version)
		}
		pins[path] = version
	}
	return pins, nil
}

// orgPinned returns the version GOORGPIN names for path, if it names one.
func orgPinned(path string) (string, bool, error) {
	pins, err := orgPins()
	if err != nil {
		return "", false, err
	}
	version, ok := pins[path]
	return version, ok, nil
}
