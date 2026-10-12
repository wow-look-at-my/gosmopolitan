// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// Package orgmod describes the modules that cmd/go resolves from a branch head
// instead of from the version token recorded in a go.mod file.
//
// A module under Prefix has no version of its own: every require line naming
// one carries a placeholder. The go command replaces that placeholder in
// memory with the pseudo-version of the head of a branch. A CI build (see
// CIBuild) takes the head its run locked (see Version), so every job of one
// run builds the same commit. The version a require line carries is therefore
// inert, which is why the token can be edited by hand. This holds by a
// released toolchain, or by a formatter without changing the build. A
// repository publishes a set of modules and pins them to one another.
// Resolving at a repository rather than at a module keeps that set on one
// commit, where a version tree cannot.
package orgmod

import (
	"strings"

	"golang.org/x/mod/module"
)

// Prefix is the module path prefix whose modules follow a branch head.
const Prefix = "github.com/wow-look-at-my/"

// IsOrg reports whether path names a module under Prefix.
func IsOrg(path string) bool {
	return strings.HasPrefix(path, Prefix)
}

// Placeholder returns the version token a go.mod file records for an org
// module: the zero version of the path's major version.
func Placeholder(path string) string {
	_, pathMajor, ok := module.SplitPathVersion(path)
	if !ok {
		return "v0.0.0"
	}
	major := strings.TrimPrefix(strings.TrimPrefix(pathMajor, "/"), ".")
	if major == "" {
		return "v0.0.0"
	}
	return major + ".0.0"
}

// Branch returns the branch named in a go.mod line's suffix comments, or "" when
// they name none. A line names a branch to send one module somewhere other than
// where the rest of them go. This covers the main module's own branch, and the
// dependency's default branch after that.
//
// The marker is read from the line the version lives on, so a fork consumed
// through a replace carries it on the replace line.
func Branch(comments []string) string {
	for _, c := range comments {
		for _, field := range strings.Split(strings.TrimPrefix(strings.TrimSpace(c), "//"), ";") {
			field = strings.TrimSpace(field)
			field = strings.TrimPrefix(field, "go-toolchain:auto-")
			field = strings.TrimPrefix(field, "go-toolchain:")
			if name, ok := strings.CutPrefix(field, "branch="); ok && name != "" {
				return name
			}
		}
	}
	return ""
}

// PlaceholderModule returns m with the version a version file records for an
// org module, and returns m unchanged for every other module.
func PlaceholderModule(m module.Version) module.Version {
	if !IsOrg(m.Path) {
		return m
	}
	m.Version = Placeholder(m.Path)
	return m
}
