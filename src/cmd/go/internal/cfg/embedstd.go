// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cfg

import (
	"fmt"
	"sync"

	"internal/cosmo/embedded"
)

// EmbeddedStd reports that this go command builds against the standard
// library embedded in its own binary: there is no GOROOT tree, every
// standard package is a compiled archive read in process, and GOROOT names
// the executable itself.
var EmbeddedStd bool

// UseEmbeddedStd puts the go command in embedded mode, with exe, this
// executable, as its GOROOT.
func UseEmbeddedStd(exe string) {
	EmbeddedStd = true
	SetGOROOT(exe, false)
}

// StdTarget names the standard library this build reads, as the blob
// files it: GOOS_GOARCH.
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
		panic(fmt.Sprintf("go: this go command embeds no standard library for %s/%s: %v", Goos, Goarch, manifestErr))
	}
	return manifest
}

// EmbeddedStdPackage answers the embedded standard package at path, or nil.
func EmbeddedStdPackage(path string) *embedded.Package {
	EmbeddedManifest()
	return manifestPkgs[path]
}

// EmbeddedStdArchive names the archive of the embedded standard package at
// path, in the form the compiler, linker and assembler open in process.
func EmbeddedStdArchive(path string) string {
	return embedded.Prefix + embedded.StdArchive(StdTarget(), path)
}

// EmbeddedIncludeDir names the assembly header directory inside this
// binary.
func EmbeddedIncludeDir() string { return embedded.Prefix + embedded.IncludeDir }
