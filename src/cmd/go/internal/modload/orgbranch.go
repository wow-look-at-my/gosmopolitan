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

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// An org module (see cmd/go/internal/orgmod) follows the head of a branch: the
// main module's checked-out branch when the dependency's repository has a
// branch of that name, and the dependency's default branch otherwise. A
// detached HEAD, or a main module that git does not track, has no branch to
// follow and so takes the default branch.
//
// Each go command resolves the head again, builds it, and writes it to go.mod
// when it moved. A readonly command writes that change too, unless an explicit
// -mod flag keeps the upstream meaning (see orgSyncing). A CI build takes the
// head its run locked instead (see orgmod.Version), so every job of one run
// builds the same commit.
//
// The resolved version lands in the root list and in every loaded go.mod
// summary, so the module graph, the build list and the module cache all see it.

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

// orgNamedBranch returns the branch the main module's go.mod names for the org
// module at path, or "" when no line names one. A name reaches every use of that
// module path, since the module graph carries one version of a path.
func orgNamedBranch(ld *Loader, path string) string {
	if ld.MainModules == nil {
		return ""
	}
	for _, v := range ld.MainModules.Versions() {
		if index := ld.MainModules.Index(v); index != nil {
			if branch := index.orgBranch[path]; branch != "" {
				return branch
			}
		}
	}
	return ""
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

// orgVersion returns the pseudo-version of the head of the branch the org
// module at path follows. In a CI build it is the head the run locked.
func orgVersion(ld *Loader, ctx context.Context, path string) (string, error) {
	// A name in go.mod replaces the branch this invocation would follow, and is
	// resolved the same way after that. A branch nothing answers for therefore
	// takes the default branch.
	branch := orgNamedBranch(ld, path)
	if branch == "" {
		branch = orgBranch(ld)
	}
	if branch == "" {
		branch = orgDefaultRev
	}
	return orgVersionCache.Do(orgVersionKey{branch, path}, func() (string, error) {
		resolve := func() (string, error) {
			version, err := orgBranchVersion(ld, ctx, path, branch)
			if err == nil || branch == orgDefaultRev {
				return version, err
			}
			// Nothing answers for that branch, so the default branch is next.
			return orgBranchVersion(ld, ctx, path, orgDefaultRev)
		}
		return orgmod.Version(ctx, orgmod.CIBuild(), orgmod.CurrentRunLock, path, branch, resolve)
	})
}

// orgBranchVersion returns the head of one branch of the repository that
// publishes path.
//
// The repository is asked before the proxy for a named branch. Query reads a
// version-shaped revision as a version query: a branch named v1 resolves to the
// newest v1 tag, or to the default branch where there is none, and never to the
// branch. Stat resolves a revision and has no such reading.
func orgBranchVersion(ld *Loader, ctx context.Context, path, branch string) (string, error) {
	repo := ld.Fetcher().Lookup(ctx, "direct", path)
	if branch != orgDefaultRev {
		if info, err := repo.Stat(ctx, branch); err == nil {
			return info.Version, nil
		}
		// A proxy reaches a module whose repository this invocation cannot.
		info, err := Query(ld, ctx, path, branch, "", nil)
		if err != nil {
			return "", err
		}
		return info.Version, nil
	}
	// "HEAD" is how git names the branch a repository starts on. The protocol a
	// proxy speaks has no spelling for it, so the repository answers first here
	// as well.
	if info, err := repo.Latest(ctx); err == nil {
		return info.Version, nil
	}
	info, err := Query(ld, ctx, path, orgDefaultRev, "", nil)
	if err != nil {
		return "", fmt.Errorf("resolving %s from the head of its default branch: %w", path, err)
	}
	return info.Version, nil
}

// orgResolvable reports whether the version of an org module is looked up at
// all in this invocation.
//
// Vendoring supplies every package from the vendor directory and the go
// command refuses to query the network in that mode. The recorded version, a
// placeholder included, is therefore the version vendored builds use, which is
// what lets a repository carry the placeholder in both its go.mod file and its
// modules.txt.
func orgResolvable() bool {
	return cfg.BuildMod != "vendor"
}

// resolveOrgRequire returns the version of m this invocation builds: the head
// of its branch, as the run locked it in a CI build. It returns m itself when m
// is not an org module, when vendoring resolves nothing, or when the main
// module replaces m with a directory.
func resolveOrgRequire(ld *Loader, ctx context.Context, m module.Version) (module.Version, error) {
	if !orgmod.IsOrg(m.Path) || !orgResolvable() {
		return m, nil
	}
	if resolvedToDirectory(ld, m) {
		// A filesystem replacement is the source of truth for this module: its
		// require line was already carrying nothing the build reads.
		return m, nil
	}
	version, err := orgVersion(ld, ctx, m.Path)
	if err != nil {
		return m, err
	}
	m.Version = version
	return m, nil
}

// resolvedToDirectory reports whether the main module replaces m with a version
// that is a directory rather than a module version.
func resolvedToDirectory(ld *Loader, m module.Version) bool {
	if ld.MainModules == nil {
		return false
	}
	repl := Replacement(ld, m)
	return repl.Path != "" && repl.Version == ""
}

// resolveOrgRequires returns mods with every org module resolved. It returns
// mods itself when no module in mods changes.
func resolveOrgRequires(ld *Loader, ctx context.Context, mods []module.Version) ([]module.Version, error) {
	out := mods
	cloned := false
	for idx, m := range mods {
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
		out[idx] = resolved
	}
	return out, nil
}

// resolveOrgSummary returns summary with the version of every org module in its
// requirements replaced by the version this invocation builds.
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

// recordOrgReplacements writes the branch head that each org replacement in
// modFile resolves to onto its replace line. The line keeps its comments. The
// require lines need no such step, because their roots are already resolved.
func recordOrgReplacements(ld *Loader, ctx context.Context, modFile *modfile.File) error {
	if !orgResolvable() {
		return nil
	}
	var stale []modfile.Replace
	for _, rep := range modFile.Replace {
		if rep.New.Version == "" || !orgmod.IsOrg(rep.New.Path) {
			continue
		}
		version, err := orgVersion(ld, ctx, rep.New.Path)
		if err != nil {
			return err
		}
		if version != rep.New.Version {
			moved := *rep
			moved.New.Version = version
			stale = append(stale, moved)
		}
	}
	for _, rep := range stale {
		if err := modFile.AddReplace(rep.Old.Path, rep.Old.Version, rep.New.Path, rep.New.Version); err != nil {
			return err
		}
	}
	return nil
}

// alignOrgVendor gives each vendored org module the version go.mod records.
// modules.txt records the placeholder for an org module, so the version the
// build list carries for it comes from go.mod. Call it after readVendorList.
func alignOrgVendor(modFiles []*modfile.File) {
	recorded := map[string]string{}
	for _, modFile := range modFiles {
		if modFile == nil {
			continue
		}
		for _, req := range modFile.Require {
			if orgmod.IsOrg(req.Mod.Path) {
				recorded[req.Mod.Path] = req.Mod.Version
			}
		}
	}
	align := func(mod module.Version) module.Version {
		if version, found := recorded[mod.Path]; found && mod.Version != "" {
			mod.Version = version
		}
		return mod
	}
	for idx, mod := range vendorList {
		vendorList[idx] = align(mod)
		if _, found := recorded[mod.Path]; found {
			vendorVersion[mod.Path] = vendorList[idx].Version
		}
	}
	for pkg, mod := range vendorPkgModule {
		vendorPkgModule[pkg] = align(mod)
	}
	for mod, meta := range vendorMeta {
		if moved := align(mod); moved != mod {
			delete(vendorMeta, mod)
			vendorMeta[moved] = meta
			if meta.GoVersion != "" {
				rawGoVersion.Store(moved, meta.GoVersion)
			}
		}
	}
}
