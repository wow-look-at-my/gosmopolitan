// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codehost

import (
	"context"
	"strings"
	"testing"
)

func TestGitRewriteTarget(t *testing.T) {
	rules := parseGitInsteadOf("url.https://mirror.example/.insteadof https://github.com/\nurl.https://golang.org/x/.insteadof https://github.com/golang/\n\n")
	cases := []struct {
		remote string
		want   string
	}{
		{"https://github.com/golang/text", "https://golang.org/x/text"},
		{"https://github.com/other/repo", "https://mirror.example/other/repo"},
		{"https://gitlab.com/some/repo", "https://gitlab.com/some/repo"},
	}
	for _, each := range cases {
		if got := gitRewriteTarget(each.remote, rules); got != each.want {
			t.Errorf("gitRewriteTarget(%q) = %q, want %q", each.remote, got, each.want)
		}
	}
}

// A remote under golang.org is refused before any VCS command runs.
func TestNewRepoRefusesGolangOrg(t *testing.T) {
	for _, remote := range []string{"https://go.golang.org/text", "https://golang.org/x/tools", "https://SUM.golang.org/"} {
		_, err := NewRepo(context.Background(), "git", remote, false)
		if err == nil || !strings.Contains(err.Error(), "golang.org") {
			t.Errorf("NewRepo(%q) err = %v, want a refusal that names golang.org", remote, err)
		}
	}
}
