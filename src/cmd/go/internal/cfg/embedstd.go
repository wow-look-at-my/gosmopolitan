// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package cfg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"internal/cosmo/embedded"
)

// EmbeddedStd reports that this go command builds against the standard library embedded in its own binary: there is no GOROOT tree.
var EmbeddedStd bool

// UseEmbeddedStd puts the go command in embedded mode, with exe, this
// executable, as its GOROOT.
func UseEmbeddedStd(exe string) {
	EmbeddedStd = true
	if tree := sameToolchainTree(os.Getenv("GOROOT")); tree != "" {
		SetGOROOT(tree, false)
		return
	}
	SetGOROOT(exe, false)
}

// sameToolchainTree answers goroot when it holds a distribution of the
// version this binary is, or "". The VERSION file's first line names it, the
// same line the distribution stamps into the binary.
func sameToolchainTree(goroot string) string {
	if goroot == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(goroot, "VERSION"))
	if err != nil {
		return ""
	}
	name, _, _ := strings.Cut(string(raw), "\n")
	if strings.TrimSpace(name) != runtime.Version() {
		return ""
	}
	if info, err := os.Stat(filepath.Join(goroot, "src", "cmd")); err != nil || !info.IsDir() {
		return ""
	}
	return goroot
}

// StdTarget names the standard library this build reads, as the blob files it: GOOS_GOARCH.
func StdTarget() string { return Goos + "_" + Goarch }

var (
	manifestOnce sync.Once
	manifest     *embedded.Manifest
	manifestErr  error
	manifestPkgs map[string]*embedded.Package
)

// EmbeddedManifest answers the embedded standard library of this build's
// target. It exits the process when the binary carries none for it.
func EmbeddedManifest() *embedded.Manifest {
	manifestOnce.Do(func() {
		manifest, manifestErr = embedded.ReadManifest(StdTarget())
		if manifestErr != nil {
			return
		}
		manifestPkgs = make(map[string]*embedded.Package, len(manifest.Packages))
		for idx := range manifest.Packages {
			manifestPkgs[manifest.Packages[idx].ImportPath] = &manifest.Packages[idx]
		}
	})
	if manifestErr != nil {
		panic(embeddedTargetMessage())
	}
	return manifest
}

func embeddedTargetMessage() string {
	return targetMessage(embeddedTargets(), Goos, Goarch, manifestErr)
}

// targetMessage says why this build has no standard library to read. Naming a
// target the binary does not carry is the reason nearly every time, and the
// reader needs the targets it does carry to see that.
func targetMessage(carried []string, goos, goarch string, err error) string {
	if len(carried) == 0 {
		return fmt.Sprintf("go: this go command carries no standard library at all, so it cannot build %s/%s: %v",
			goos, goarch, err)
	}
	for _, target := range carried {
		if target == goos+"/"+goarch {
			return fmt.Sprintf("go: this go command carries a standard library for %s/%s and cannot read it: %v", goos, goarch, err)
		}
	}
	return fmt.Sprintf("go: this go command builds for %s, and GOOS=%s GOARCH=%s names %s/%s instead.\n"+
		"\tIt carries a standard library for those targets alone, so there is nothing here to compile %s/%s against.\n"+
		"\tLeave GOOS and GOARCH unset: the target of this go command is already the one it can build.",
		strings.Join(carried, " and "), goos, goarch, goos, goarch, goos, goarch)
}

// embeddedTargets lists the targets this binary carries, as GOOS/GOARCH.
func embeddedTargets() []string {
	names, err := embedded.Entries(embedded.ManifestEntry(""))
	if err != nil {
		return nil
	}
	targets := make([]string, 0, len(names))
	for _, name := range names {
		target := strings.TrimPrefix(name, embedded.ManifestEntry(""))
		targets = append(targets, strings.Replace(target, "_", "/", 1))
	}
	return targets
}

// EmbeddedStdPackage answers the embedded standard package at path, or nil.
func EmbeddedStdPackage(path string) *embedded.Package {
	EmbeddedManifest()
	return manifestPkgs[path]
}

// EmbeddedStdArchived answers the embedded standard package at path when this
// binary carries its archive, or nil.
func EmbeddedStdArchived(path string) *embedded.Package {
	pkg := EmbeddedStdPackage(path)
	if pkg == nil || pkg.Archive == "" {
		return nil
	}
	return pkg
}

// EmbeddedStdArchive names the archive of the embedded standard package at
// path, in the form the compiler, linker and assembler open.
func EmbeddedStdArchive(path string) string {
	return embedded.Prefix + embedded.StdArchive(StdTarget(), path)
}

// EmbeddedIncludeDir names the assembly header directory inside this binary.
func EmbeddedIncludeDir() string { return embedded.Prefix + embedded.IncludeDir }
