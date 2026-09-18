// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package vendorlist answers which packages a vendor tree vendors.
//
// A vendor directory in this distribution holds whole repositories,
// checked out as git submodules, so it also carries packages nothing in
// the distribution imports and whose own dependencies were never
// vendored. modules.txt names the packages that are part of the build;
// the rest is checkout residue.
package vendorlist

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// lists caches one vendor tree's package set, by its directory. A nil
// set means the tree has no modules.txt and makes no claim.
var lists sync.Map

// Vendors reports whether the distribution vendors the package in dir.
// A directory outside any vendor tree is the distribution's own, and so
// always vendored.
func Vendors(dir string) bool {
	parts := strings.Split(filepath.ToSlash(dir), "/")
	i := slices.Index(parts, "vendor")
	if i < 0 {
		return true
	}
	vendorDir := filepath.FromSlash(strings.Join(parts[:i+1], "/"))

	set, ok := lists.Load(vendorDir)
	if !ok {
		set, _ = lists.LoadOrStore(vendorDir, read(vendorDir))
	}
	pkgs, _ := set.(map[string]bool)
	if pkgs == nil {
		return true
	}
	return pkgs[strings.Join(parts[i+1:], "/")]
}

// read reads the package paths a vendor tree's modules.txt names. It
// answers nil when the file is absent, which is not an error.
func read(vendorDir string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(vendorDir, "modules.txt"))
	if err != nil {
		return nil
	}
	pkgs := make(map[string]bool)
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			pkgs[line] = true
		}
	}
	return pkgs
}
