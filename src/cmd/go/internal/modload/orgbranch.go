// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modload

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/orgmod"
	"cmd/internal/par"

	"golang.org/x/mod/module"
)

// An org module (see cmd/go/internal/orgmod) carries no version of its own.
// The token on its require line is a placeholder, and the go command replaces
// that token in memory with the pseudo-version of the head of a branch: the
// main module's checked-out branch when the dependency's repository has a
// branch of that name, and the dependency's default branch otherwise. A
// detached HEAD, or a main module that git does not track, has no branch to
// follow and so takes the default branch.
//
// The resolved version lands in the root list and in every loaded go.mod
// summary, so the module graph, the build list and the module cache all see it
// and MVS can compare it against the requirements of other modules. The files
// on disk keep the placeholder: a writer emits the placeholder (see
// orgmod.Placeholder), so a repository can be edited, formatted or tidied
// without recording a commit that freezes one repository against another.

// orgDefaultRev is the revision that names a repository's default branch.
// git resolves HEAD at the remote to the head of the default branch, so one
// query covers every repository without asking which branch it prefers.
const orgDefaultRev = "HEAD"

// orgBranchCache memoizes the checked-out branch of a directory, so that a
// single invocation runs git at most once for the main module.
var orgBranchCache par.Cache[string, string] // module root dir → branch ("" if none)

// orgVersionKey identifies one resolution: the module path, whose major version
// the pseudo-version must carry, and the branch it was resolved against, since
// a loader can be re-rooted onto a different main module within an invocation.
type orgVersionKey struct {
	branch string
	path   string
}

// orgVersionCache memoizes the resolved version of an org module, the way
// @latest lookups are cached. Every module in a repository still resolves
// through one repository object, and so through one ls-remote.
var orgVersionCache par.ErrCache[orgVersionKey, string] // branch, module path → version

// orgBranch returns the branch that org modules follow in this invocation, or
// "" when the main module has no checked-out branch.
func orgBranch(ld *Loader) string {
	dir := orgMainDir(ld)
	if dir == "" {
		return ""
	}
	return orgBranchCache.Do(dir, func() string { return gitCheckedOutBranch(dir) })
}

// orgMainDir returns the directory of the main module that contains the
// current directory, or of the first main module when none does. In workspace
// mode the two can differ; the module the command was run in decides which
// branch the workspace's org dependencies follow.
func orgMainDir(ld *Loader) string {
	cwd := base.Cwd()
	sep := string(filepath.Separator)
	first := ""
	best := ""
	for _, dir := range ld.modRoots {
		if dir == "" {
			continue
		}
		if first == "" {
			first = dir
		}
		if cwd == dir || strings.HasPrefix(cwd, dir+sep) {
			if len(dir) > len(best) {
				best = dir
			}
		}
	}
	if best != "" {
		return best
	}
	return first
}

// gitCheckedOutBranch returns the branch checked out in the git repository
// containing dir, or "" if there is none: dir is not in a repository, git is
// not installed, or HEAD is detached.
func gitCheckedOutBranch(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		// A repository we cannot read tells us nothing about which branch to
		// follow, and that is not a reason to fail the build.
		return ""
	}
	// A detached HEAD reports itself as "HEAD", which names no branch.
	name := strings.TrimSpace(string(out))
	if name == "" || name == "HEAD" {
		return ""
	}
	return name
}

// orgVersion returns the version of the org module at path: the pseudo-version
// of the head of the branch it follows. The version token on any require line
// naming path is neither read nor consulted.
func orgVersion(ld *Loader, ctx context.Context, path string) (string, error) {
	branch := orgBranch(ld)
	if branch == "" {
		branch = orgDefaultRev
	}
	return orgVersionCache.Do(orgVersionKey{branch, path}, func() (string, error) {
		info, err := Query(ld, ctx, path, branch, "", nil)
		if err != nil && branch != orgDefaultRev {
			// The repository has no branch by that name, so it takes the head of
			// its default branch instead.
			info, err = Query(ld, ctx, path, orgDefaultRev, "", nil)
		}
		if err != nil {
			return "", fmt.Errorf("resolving %s from the head of %s: %w", path, branch, err)
		}
		return info.Version, nil
	})
}

// orgResolvable reports whether the branch head of an org module can be
// resolved in this invocation.
//
// Vendoring supplies every package from the vendor directory and the go
// command refuses to query the network in that mode. The placeholder is
// therefore the version vendored builds use, which is what lets a repository
// carry the placeholder in both its go.mod file and its modules.txt.
func orgResolvable() bool {
	return cfg.BuildMod != "vendor"
}

// resolveOrgRequire returns m with the head of the branch it resolves to, or m
// itself when m is not an org module or this invocation cannot resolve one.
// The token m carries is not read.
func resolveOrgRequire(ld *Loader, ctx context.Context, m module.Version) (module.Version, error) {
	if !orgmod.IsOrg(m.Path) || !orgResolvable() {
		return m, nil
	}
	version, err := orgVersion(ld, ctx, m.Path)
	if err != nil {
		return m, err
	}
	m.Version = version
	return m, nil
}

// resolveOrgRequires returns mods with every org module resolved. It returns
// mods itself when no module in mods changes.
func resolveOrgRequires(ld *Loader, ctx context.Context, mods []module.Version) ([]module.Version, error) {
	out := mods
	cloned := false
	for i, m := range mods {
		resolved, err := resolveOrgRequire(ld, ctx, m)
		if err != nil {
			return nil, err
		}
		if resolved == m {
			continue
		}
		if !cloned {
			out = append([]module.Version(nil), mods...)
			cloned = true
		}
		out[i] = resolved
	}
	return out, nil
}

// resolveOrgSummary returns summary with the version of every org module in its
// requirements replaced by the head of the branch it resolves to.
//
// rawGoModSummary is reached through the context-free mvs.Reqs interface, so
// there is no caller context to pass down here. rawGoModData reads the go.mod
// file itself the same way.
func resolveOrgSummary(ld *Loader, summary *modFileSummary) (*modFileSummary, error) {
	if summary == nil || len(summary.require) == 0 {
		return summary, nil
	}
	reqs, err := resolveOrgRequires(ld, context.TODO(), summary.require)
	if err != nil {
		return nil, err
	}
	summary.require = reqs
	return summary, nil
}
