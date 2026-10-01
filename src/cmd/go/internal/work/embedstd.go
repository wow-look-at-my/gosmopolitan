// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

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
// A package the manifest never names answers nil and compiles here like any
// other, rather than claiming an archive that is not in it. cmd is such a
// package set. That package needs a tree to compile from, and a binary with
// neither the archive nor a tree says which package it wanted.
func (builder *Builder) embeddedStdAction(act *Action, p *load.Package) *Action {
	pkg := cfg.EmbeddedStdPackage(p.ImportPath)
	outcome := embeddedStdLookup(pkg)
	if outcome == embeddedStdFromTree {
		if info, err := os.Stat(cfg.GOROOTsrc); err != nil || !info.IsDir() {
			base.Fatalf("go: %s: this go command embeds no such standard package for %s/%s, and GOROOT %s holds no source to compile it from", p.ImportPath, cfg.Goos, cfg.Goarch, cfg.GOROOT)
		}
		return nil
	}
	act.Mode = "embedded std"
	act.Actor = nil
	if outcome == embeddedStdNoArchive {
		return act
	}
	act.Target = cfg.EmbeddedStdArchive(p.ImportPath)
	act.built = act.Target
	act.buildID = pkg.BuildID
	if builder.NeedExport {
		// No build runs for this action, so the listing's answer is filled
		// in here, where a compile's cache hit would fill it.
		act.built = embeddedStdFile(p.ImportPath, pkg)
		p.Export = act.built
		p.BuildID = act.buildID
	}
	return act
}

// buildIDActionBytes decodes the ACTION field of a build id -- the part
// before the slash -- which cmd/go writes as the leading bytes of the action
// id that produced the object, base64.RawURLEncoding'd. It answers nil when
// the build id carries no decodable action.
func buildIDActionBytes(buildID string) []byte {
	action, _, ok := strings.Cut(buildID, "/")
	if !ok {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(action)
	if err != nil {
		return nil
	}
	return raw
}

// embeddedStdKey answers the cache key for an embedded std archive, with the
// archive's OWN action bytes at the front.
//
// A shared cache certifies an entry by checking the object's build id against
// the key it arrived under, so a key that is a plain content hash can never
// match an archive compiled in another build, and every embedded std package
// is refused on write and scored corrupt on read. Leading with the archive's
// action makes that check pass for the same reason it passes for a compile
// output: the key names the action the object came from. The rest of the key
// stays the content hash, so two archives never share one.
func embeddedStdKey(content [cache.HashSize]byte, buildID string) [cache.HashSize]byte {
	action := buildIDActionBytes(buildID)
	if len(action) == 0 || len(action) >= cache.HashSize {
		return content
	}
	key := content
	copy(key[:len(action)], action)
	return key
}

// embeddedStdFile answers a file holding the embedded archive of a
// standard package, written into the build cache the first time a process
// that cannot read this binary asks for it.
func embeddedStdFile(importPath string, pkg *embedded.Package) string {
	hash := cache.NewHash("embedded std archive")
	fmt.Fprintf(hash, "%s %s %s\n", cfg.StdTarget(), importPath, pkg.BuildID)
	key := embeddedStdKey(hash.Sum(), pkg.BuildID)
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
	pkg := cfg.EmbeddedStdArchived(p.ImportPath)
	if pkg == nil {
		return built
	}
	return embeddedStdFile(p.ImportPath, pkg)
}

// embeddedStdOutcome says what a manifest lookup leaves embeddedStdAction to
// do. The two failing cases are separate answers on purpose: a package the
// manifest never names needs a tree, and a package it names with no archive
// needs nothing at all. Reading both as "absent" is what stopped `go list
// std` under a binary that carries no tree.
type embeddedStdOutcome int

const (
	embeddedStdFromTree  embeddedStdOutcome = iota // unnamed by the manifest: compile it from a GOROOT tree
	embeddedStdNoArchive                           // named with no archive: nothing to compile and nothing to read
	embeddedStdArchive                             // named with an archive this binary carries
)

func embeddedStdLookup(pkg *embedded.Package) embeddedStdOutcome {
	switch {
	case pkg == nil:
		return embeddedStdFromTree
	case !servesArchive(pkg):
		return embeddedStdNoArchive
	}
	return embeddedStdArchive
}

// servesArchive reports that the manifest entry carries a compiled archive to
// read. A standard package of test files alone, crypto/internal/fips140test
// among them, compiles to none, and embedstd records the entry with an empty
// archive name. Asking the blob for that name finds no entry and stops the go
// command, which is how `go list std` died under a binary carrying one.
func servesArchive(pkg *embedded.Package) bool {
	return pkg != nil && pkg.Archive != ""
}
