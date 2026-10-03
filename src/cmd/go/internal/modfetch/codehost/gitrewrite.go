// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codehost

import (
	"os/exec"
	"strings"
	"sync"
)

// gitInsteadOf lists the URL prefixes that a url.<base>.insteadOf rule in the
// git config rewrites. git reads the config for itself, so the list is read
// once per process.
var gitInsteadOf = sync.OnceValue(func() []string {
	out, err := exec.Command("git", "config", "--get-regexp", `^url\..*\.insteadof$`).Output()
	if err != nil {
		// No git, or no rule. Either way git rewrites nothing.
		return nil
	}
	var prefixes []string
	for _, line := range strings.Split(string(out), "\n") {
		if _, prefix, ok := strings.Cut(strings.TrimSpace(line), " "); ok && prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
})

// gitRewrites reports whether git sends remote somewhere else. The github.com
// shortcut then must not run, because it would go around that rule.
func gitRewrites(remote string) bool {
	for _, prefix := range gitInsteadOf() {
		if strings.HasPrefix(remote, prefix) {
			return true
		}
	}
	return false
}
