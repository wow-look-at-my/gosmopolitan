// Package submodulebranch answers which branch of a submodule this repository
// follows: the branch of its own name when the submodule has one, and the
// default branch otherwise.
//
// `branch = .` in .gitmodules says the first half to git. It has no second
// half: a submodule with no branch of that name makes `git submodule update
// --remote` fail rather than fall back.
package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs a git command and answers its standard output. A caller supplies
// one so a test never reaches the network.
type Runner func(args ...string) ([]byte, error)

// Git runs git for real.
func Git(args ...string) ([]byte, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// Here is the branch this repository is on, and "" when there is none: a
// detached HEAD, or no repository. Both leave nothing to match.
func Here(run Runner) string {
	out, err := run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	if branch := strings.TrimSpace(string(out)); branch != "HEAD" {
		return branch
	}
	return ""
}

// Resolve answers the ref a submodule at url follows, and the branch name it
// settled on. An empty name is the default branch, which the ref spells HEAD.
//
// One ls-remote asks for both refs. A remote that cannot answer, a submodule
// without the branch, and a submodule whose default branch IS that branch all
// mean the same thing.
func Resolve(run Runner, url, here string) (ref, branch string) {
	if here == "" {
		return "HEAD", ""
	}
	want := "refs/heads/" + here
	out, err := run("ls-remote", "--symref", url, "HEAD", want)
	if err != nil {
		return "HEAD", ""
	}
	refs, def := parse(out)
	if refs[want] == "" || def == here {
		return "HEAD", ""
	}
	return want, here
}

// parse reads ls-remote --symref output. The symref line names the default
// branch. Each other line is a hash and the ref it names.
func parse(out []byte) (refs map[string]string, def string) {
	refs = map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "ref: "); ok {
			target, name, found := strings.Cut(rest, "\t")
			if found && strings.TrimSpace(name) == "HEAD" {
				def = strings.TrimPrefix(strings.TrimSpace(target), "refs/heads/")
			}
			continue
		}
		hash, name, found := strings.Cut(line, "\t")
		if !found {
			continue
		}
		refs[strings.TrimSpace(name)] = strings.TrimSpace(hash)
	}
	return refs, def
}
