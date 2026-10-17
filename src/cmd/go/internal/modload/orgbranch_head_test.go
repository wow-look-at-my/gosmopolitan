// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modload

import (
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadGitHeadBranchMatchesGit holds the HEAD file read to the answer
// git rev-parse --abbrev-ref HEAD gives, in each layout a module can sit in.
func TestReadGitHeadBranchMatchesGit(t *testing.T) {
	testenv.MustHaveExecPath(t, "git")
	if os.Getenv("GIT_DIR") != "" || os.Getenv("GIT_WORK_TREE") != "" {
		t.Fatal("GIT_DIR or GIT_WORK_TREE is set, so the HEAD read defers to git and the test proves nothing")
	}
	root := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o777); err != nil {
		t.Fatal(err)
	}
	git(repo, "init", "-q", "-b", "claude/some-branch")
	git(repo, "commit", "-q", "--allow-empty", "-m", "first")
	worktree := filepath.Join(root, "worktree")
	git(repo, "worktree", "add", "-q", "-b", "other", worktree)
	detached := filepath.Join(root, "detached")
	git(repo, "worktree", "add", "-q", "--detach", detached)
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o777); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		dir  string
		want string
	}{
		{repo, "claude/some-branch"},
		{sub, "claude/some-branch"},
		{worktree, "other"},
		{detached, ""},
	} {
		got, ok := readGitHeadBranch(tc.dir)
		if !ok || got != tc.want {
			t.Errorf("readGitHeadBranch(%s) = %q, %v; want %q, true", tc.dir, got, ok, tc.want)
		}
		if fromGit := gitCheckedOutBranch(tc.dir); fromGit != tc.want {
			t.Errorf("gitCheckedOutBranch(%s) = %q; want %q", tc.dir, fromGit, tc.want)
		}
	}
	if got, ok := readGitHeadBranch(outside); ok && got != "" {
		t.Errorf("readGitHeadBranch(%s) = %q outside any repository", outside, got)
	}
}
