// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modload

import (
	"strings"

	"cmd/go/internal/cfg"
	"cmd/go/internal/gover"
	"cmd/go/internal/orgmod"
	"cmd/internal/par"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// Outside CI an org module moves to its branch head, so a run can record a new
// version for it, and its new commit can require a new module. The org already
// vetted both, so a readonly command writes them without a prompt when they
// are the whole change. Any other change still fails as -mod=readonly does
// upstream. See docs/ORG-DEPS.md.

// orgSyncing reports whether this invocation may record org module versions
// and what org modules declare. An explicit -mod flag keeps its upstream
// meaning.
func orgSyncing(ld *Loader) bool {
	return cfg.BuildMod == "readonly" && !cfg.BuildModExplicit &&
		ld.HasModRoot() && !ld.inWorkspaceMode() && orgResolvable()
}

// orgDeclaredCache maps a module path to the version org modules require.
var orgDeclaredCache par.Cache[*Loader, map[string]string]

// orgDeclared returns the version that org modules require for path, or "".
func orgDeclared(ld *Loader, path string) string {
	if ld.requirements == nil {
		return ""
	}
	decl := orgDeclaredCache.Do(ld, func() map[string]string {
		return collectOrgDeclared(ld, ld.requirements.rootModules)
	})
	return decl[path]
}

// collectOrgDeclared reads the go.mod file of each org module in roots, and
// of each org module those require. The highest version wins.
func collectOrgDeclared(ld *Loader, roots []module.Version) map[string]string {
	decl := map[string]string{}
	seen := map[module.Version]bool{}
	queue := append([]module.Version(nil), roots...)
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		if !orgmod.IsOrg(m.Path) || seen[m] || ld.MainModules.Contains(m.Path) {
			continue
		}
		seen[m] = true
		summary, err := goModSummary(ld, m)
		if err != nil || summary == nil {
			// The graph load reports this error.
			continue
		}
		for _, r := range summary.require {
			if orgmod.IsOrg(r.Path) {
				queue = append(queue, r)
				continue
			}
			if old, ok := decl[r.Path]; !ok || gover.ModCompare(r.Path, old, r.Version) < 0 {
				decl[r.Path] = r.Version
			}
		}
	}
	return decl
}

// orgSyncAllows reports whether m, or its go.mod file, is at the version an
// org module declares. A readonly run may then verify its checksum against the
// checksum database and record it.
func orgSyncAllows(ld *Loader, m module.Version) bool {
	if !orgSyncing(ld) || orgmod.IsOrg(m.Path) {
		return false
	}
	version := strings.TrimSuffix(m.Version, "/go.mod")
	return version != "" && orgDeclared(ld, m.Path) == version
}

// orgMoved reports whether a line for old may become a line for moved because
// an org module moved to its branch head. A CI build moves nothing.
func orgMoved(old, moved module.Version) bool {
	return orgFollowsBranch() && orgmod.IsOrg(old.Path) && old.Path == moved.Path &&
		old.Version != "" && moved.Version != ""
}

// orgOnlyChange reports whether modFile differs from i only by what org
// modules explain: an org module moved to its branch head, and an indirect
// requirement an org module declares, added or raised to that version.
func orgOnlyChange(ld *Loader, i *modFileIndex, modFile *modfile.File) bool {
	if i == nil || i.dataNeedsFix || modFile.Module == nil || modFile.Module.Mod != i.module {
		return false
	}
	var goV, toolchain string
	if modFile.Go != nil {
		goV = modFile.Go.Version
	}
	if modFile.Toolchain != nil {
		toolchain = modFile.Toolchain.Name
	}
	if goV != i.goVersion || toolchain != i.toolchain ||
		len(modFile.Replace) != len(i.replace) || len(modFile.Exclude) != len(i.exclude) {
		return false
	}
	for _, r := range modFile.Replace {
		if old := i.replace[r.Old]; r.New != old && !orgMoved(old, r.New) {
			return false
		}
	}
	for _, x := range modFile.Exclude {
		if !i.exclude[x.Mod] {
			return false
		}
	}

	oldPaths := map[string]bool{}
	for mod := range i.require {
		oldPaths[mod.Path] = true
	}
	kept := map[module.Version]bool{}
	raised := map[string]bool{}
	for _, r := range modFile.Require {
		if _, ok := i.require[r.Mod]; ok {
			kept[r.Mod] = true
			continue
		}
		if orgmod.IsOrg(r.Mod.Path) && orgFollowsBranch() && (r.Indirect || oldPaths[r.Mod.Path]) {
			// The version is the branch head this run resolved.
			raised[r.Mod.Path] = true
			continue
		}
		if !r.Indirect || orgmod.IsOrg(r.Mod.Path) || orgDeclared(ld, r.Mod.Path) != r.Mod.Version {
			return false
		}
		raised[r.Mod.Path] = true
	}
	// A line may leave only when a line at the new version replaces it.
	for m := range i.require {
		if !kept[m] && !raised[m.Path] {
			return false
		}
	}
	return true
}

// orgSyncCommit reports whether a readonly run may write go.mod and go.sum,
// because org modules explain the whole change.
func orgSyncCommit(ld *Loader, i *modFileIndex, modFile *modfile.File) bool {
	return orgSyncing(ld) && orgOnlyChange(ld, i, modFile)
}
