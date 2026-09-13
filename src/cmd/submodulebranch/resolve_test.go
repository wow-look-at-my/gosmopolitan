package main

import (
	"errors"
	"testing"
)

// lsRemote answers what `git ls-remote --symref <url> HEAD refs/heads/<b>`
// prints when the remote's default branch is def and it carries have.
func lsRemote(def string, have ...string) Runner {
	return func(args ...string) ([]byte, error) {
		out := "ref: refs/heads/" + def + "\tHEAD\n" +
			"1111111111111111111111111111111111111111\tHEAD\n"
		for _, b := range have {
			out += "2222222222222222222222222222222222222222\trefs/heads/" + b + "\n"
		}
		return []byte(out), nil
	}
}

func TestFollowsTheMatchingBranch(t *testing.T) {
	ref, branch := Resolve(lsRemote("master", "master", "claude/feature"), "u", "claude/feature")
	if ref != "refs/heads/claude/feature" || branch != "claude/feature" {
		t.Fatalf("got %q %q, want the matching branch", ref, branch)
	}
}

// The case `branch = .` cannot express: the submodule has no branch of this
// name, so it takes the default rather than failing.
func TestFallsBackWhenTheBranchIsAbsent(t *testing.T) {
	ref, branch := Resolve(lsRemote("master", "master"), "u", "claude/feature")
	if ref != "HEAD" || branch != "" {
		t.Fatalf("got %q %q, want the default branch", ref, branch)
	}
}

// A submodule whose default branch IS this name follows the default, so the
// resolved ref does not depend on which of the two spellings git reports.
func TestDefaultBranchOfThatNameIsTheDefault(t *testing.T) {
	ref, branch := Resolve(lsRemote("master", "master"), "u", "master")
	if ref != "HEAD" || branch != "" {
		t.Fatalf("got %q %q, want the default branch", ref, branch)
	}
}

// A remote that cannot answer must not fail the build.
func TestUnreachableRemoteTakesTheDefault(t *testing.T) {
	fail := func(args ...string) ([]byte, error) { return nil, errors.New("no network") }
	ref, branch := Resolve(fail, "u", "claude/feature")
	if ref != "HEAD" || branch != "" {
		t.Fatalf("got %q %q, want the default branch", ref, branch)
	}
}

// A detached HEAD leaves nothing to match, and asks the remote nothing.
func TestDetachedHeadMatchesNothing(t *testing.T) {
	asked := false
	watch := func(args ...string) ([]byte, error) {
		asked = true
		return nil, nil
	}
	ref, branch := Resolve(watch, "u", "")
	if ref != "HEAD" || branch != "" {
		t.Fatalf("got %q %q, want the default branch", ref, branch)
	}
	if asked {
		t.Fatal("asked the remote with no branch to match")
	}
}

func TestHereReadsTheBranch(t *testing.T) {
	on := func(args ...string) ([]byte, error) { return []byte("claude/feature\n"), nil }
	if got := Here(on); got != "claude/feature" {
		t.Fatalf("got %q", got)
	}
}

func TestHereAnswersEmptyOnDetachedHead(t *testing.T) {
	detached := func(args ...string) ([]byte, error) { return []byte("HEAD\n"), nil }
	if got := Here(detached); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestHereAnswersEmptyOutsideARepository(t *testing.T) {
	fail := func(args ...string) ([]byte, error) { return nil, errors.New("not a repository") }
	if got := Here(fail); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestParseReadsRefsAndDefault(t *testing.T) {
	out, _ := lsRemote("master", "master", "claude/feature")()
	refs, def := parse(out)
	if def != "master" {
		t.Fatalf("default %q", def)
	}
	if refs["refs/heads/claude/feature"] == "" {
		t.Fatal("the matching branch is missing")
	}
}
