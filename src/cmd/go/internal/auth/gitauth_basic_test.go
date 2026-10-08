// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package auth

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGitBasicAuthReadsTheGitCredential covers the credential a caller that
// would otherwise run git presents to a plain HTTPS request. The helper is
// git's own store, so the test owns every file git reads.
func TestGitBasicAuthReadsTheGitCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ASKPASS", "")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	if err := os.WriteFile(filepath.Join(home, "gitconfig"), []byte("[credential]\n\thelper = store\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".git-credentials"), []byte("https://gopher:sekret@github.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cred, err := GitBasicAuth(t.TempDir(), "https://github.com/wow-look-at-my/private")
	if err != nil {
		t.Fatalf("GitBasicAuth = %v; want the stored credential", err)
	}
	password, _ := cred.Password()
	if cred.Username() != "gopher" || password != "sekret" {
		t.Errorf("GitBasicAuth = %q:%q; want gopher:sekret", cred.Username(), password)
	}
}

// TestGitBasicAuthWithoutACredential reports the absence rather than an empty
// credential, so the caller can fall back to git itself.
func TestGitBasicAuthWithoutACredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ASKPASS", "")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	if err := os.WriteFile(filepath.Join(home, "gitconfig"), []byte("[credential]\n\thelper = store\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := GitBasicAuth(t.TempDir(), "https://github.com/wow-look-at-my/private"); err == nil {
		t.Error("GitBasicAuth with no stored credential = nil error; want the absence reported")
	}
}
