// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"fmt"

	"cmd/go/internal/base"
	"cmd/go/internal/cache"
	"cmd/go/internal/cfg"
	"cmd/go/internal/load"
	"internal/cosmo/embedded"
)

// embeddedStdAction finishes the compile action of a standard package in
// embedded mode: the archive already exists inside this binary, so the
// action has nothing to run and answers the archive's name and the build ID
// recorded for it. A reader outside this process, which a -export listing
// serves, gets the archive as a build cache file instead.
func (builder *Builder) embeddedStdAction(act *Action, p *load.Package) *Action {
	pkg := cfg.EmbeddedStdPackage(p.ImportPath)
	if pkg == nil {
		base.Fatalf("go: %s: this go command embeds no such standard package for %s/%s", p.ImportPath, cfg.Goos, cfg.Goarch)
	}
	act.Mode = "embedded std"
	act.Actor = nil
	act.Target = cfg.EmbeddedStdArchive(p.ImportPath)
	act.built = act.Target
	act.buildID = pkg.BuildID
	if builder.NeedExport {
		act.built = embeddedStdFile(p.ImportPath, pkg)
	}
	return act
}

// embeddedStdFile answers a file holding the embedded archive of a
// standard package, written into the build cache the first time a process
// that cannot read this binary asks for it.
func embeddedStdFile(importPath string, pkg *embedded.Package) string {
	hash := cache.NewHash("embedded std archive")
	fmt.Fprintf(hash, "%s %s %s\n", cfg.StdTarget(), importPath, pkg.BuildID)
	key := hash.Sum()
	store := cache.Default()
	if file, _, err := cache.GetFile(store, key); err == nil {
		return file
	}
	data, err := embedded.ReadFile(pkg.Archive)
	if err != nil {
		base.Fatalf("go: %s: %v", importPath, err)
	}
	out, _, err := store.Put(key, bytes.NewReader(data))
	if err != nil {
		base.Fatalf("go: %s: writing the embedded archive to the build cache: %v", importPath, err)
	}
	return store.OutputFile(out)
}

// fileForOutsideReader answers built as a path another process can open:
// built itself for a file, and the build cache copy for an archive inside
// this binary.
func fileForOutsideReader(p *load.Package, built string) string {
	if !cfg.EmbeddedStd || !embedded.IsSelf(built) {
		return built
	}
	pkg := cfg.EmbeddedStdPackage(p.ImportPath)
	if pkg == nil {
		return built
	}
	return embeddedStdFile(p.ImportPath, pkg)
}
