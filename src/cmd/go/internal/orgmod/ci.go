// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package orgmod

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// CIBuild reports whether this process builds the org module versions that
// go.mod records, as a CI job does. Every other go command moves each org
// module to the head of its branch and records that version in go.mod.
//
// Only CI builds the recorded version. A coding agent that could claim to be CI
// could hold an org module at an old commit to dodge a new one, so the answer is
// false under an agent, whatever GITHUB_ACTIONS says. No other switch exists.
var CIBuild = sync.OnceValue(func() bool {
	return ciBuild(os.Getenv, AgentAncestor)
})

// ciBuild reports whether getenv describes a CI job with no coding agent in the
// environment and none among the ancestor processes that ancestor names.
func ciBuild(getenv func(string) string, ancestor func() string) bool {
	return getenv("GITHUB_ACTIONS") == "true" && agentMarker(getenv) == "" && ancestor() == ""
}

// agentMarker returns the name of an environment variable that says a coding
// agent runs this process, or "". A marker set to "0" says the agent is absent.
func agentMarker(getenv func(string) string) string {
	for _, name := range agentEnvMarkers {
		if val := getenv(name); val != "" && val != "0" {
			return name
		}
	}
	return ""
}

// These lists copy github.com/wow-look-at-my/is-this-an-agent, because the toolchain vendors no new dependency.
var (
	agentEnvMarkers   = []string{"CLAUDECODE", "GROK_AGENT", "CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "GEMINI_CLI", "OPENCODE"}
	agentProcPrefixes = []string{"claude", "grok", "xai-grok-pager", "codex", "gemini", "opencode"}
)

// AgentAncestor returns the name of an ancestor process that is a coding agent,
// or "". It reads /proc, so it finds nothing where /proc is absent.
func AgentAncestor() string {
	pid := os.Getppid()
	for depth := 0; pid > 1 && depth < 64; depth++ {
		comm, ppid, found := procParent(pid)
		if !found {
			return ""
		}
		for _, prefix := range agentProcPrefixes {
			if strings.HasPrefix(comm, prefix) {
				return comm
			}
		}
		pid = ppid
	}
	return ""
}

// procParent reads the command name and parent pid of pid from /proc.
func procParent(pid int) (comm string, ppid int, found bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false
	}
	// The command name sits in parentheses and may hold spaces or ")".
	stat := string(data)
	open := strings.IndexByte(stat, '(')
	shut := strings.LastIndexByte(stat, ')')
	if open < 0 || shut < open {
		return "", 0, false
	}
	fields := strings.Fields(stat[shut+1:])
	if len(fields) < 2 {
		return "", 0, false
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, false
	}
	return stat[open+1 : shut], ppid, true
}
