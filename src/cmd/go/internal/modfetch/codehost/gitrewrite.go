// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// gitRule is a url.<base>.insteadOf rule: git sends a URL that starts with prefix to base instead.

package codehost

import (
	"os/exec"
	"strings"
	"sync"
)

// gitRule is a url.<base>.insteadOf rule: git sends a URL that starts with
// prefix to base instead.
type gitRule struct {
	base   string
	prefix string
}

// gitInsteadOf lists the url.<base>.insteadOf rules in the git config. git
// reads the config for itself, so the list is read once per process.
var gitInsteadOf = sync.OnceValue(func() []gitRule {
	out, err := exec.Command("git", "config", "--get-regexp", `^url\..*\.insteadof$`).Output()
	if err != nil {
		// No git, or no rule. Either way git rewrites nothing.
		return nil
	}
	return parseGitInsteadOf(string(out))
})

// parseGitInsteadOf reads the output of git config --get-regexp for the
// insteadOf keys.
func parseGitInsteadOf(out string) []gitRule {
	var rules []gitRule
	for _, line := range strings.Split(out, "\n") {
		key, prefix, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || prefix == "" {
			continue
		}
		base := strings.TrimSuffix(strings.TrimPrefix(key, "url."), ".insteadof")
		rules = append(rules, gitRule{base: base, prefix: prefix})
	}
	return rules
}

// gitRewrites reports whether git sends remote somewhere else. The github.com
// shortcut then must not run, because it would go around that rule.
func gitRewrites(remote string) bool {
	for _, rule := range gitInsteadOf() {
		if strings.HasPrefix(remote, rule.prefix) {
			return true
		}
	}
	return false
}

// gitRewriteTarget answers the URL git uses for remote after the rules. git
// applies the rule with the longest matching prefix.
func gitRewriteTarget(remote string, rules []gitRule) string {
	var best gitRule
	for _, rule := range rules {
		if strings.HasPrefix(remote, rule.prefix) && len(rule.prefix) > len(best.prefix) {
			best = rule
		}
	}
	if best.prefix == "" {
		return remote
	}
	return best.base + strings.TrimPrefix(remote, best.prefix)
}
