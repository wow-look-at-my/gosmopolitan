// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package orgmod describes the modules that cmd/go resolves from a branch head
// instead of from the version token recorded in a go.mod file.
//
// A module under Prefix has no version of its own: every require line naming
// one carries a placeholder, and the go command replaces that placeholder in
// memory with the pseudo-version of the head of a branch. The version a require
// line carries is therefore inert, which is why the token can be edited by
// hand, by a released toolchain, or by a formatter without changing the build.
// A repository publishes a set of modules and pins them to one another;
// resolving at a repository rather than at a module keeps that set on one
// commit, where a version tree cannot.
package orgmod

import (
	"strings"

	"golang.org/x/mod/module"
)

// Prefix is the module path prefix whose modules follow a branch head.
const Prefix = "github.com/wow-look-at-my/"

// IsOrg reports whether path names a module under Prefix.
//
// Every writer, every go.sum guard, and the vendor consistency check asks this
// one question, so the rule cannot drift between them.
func IsOrg(path string) bool {
	return strings.HasPrefix(path, Prefix)
}

// Placeholder returns the version token a go.mod file records for an org
// module: vN.0.0 for the major version of path, so v0.0.0 for a path with no
// major version suffix and v2.0.0 for one ending in /v2.
//
// The token is valid semver, so a released toolchain reads it without
// complaint, and stable, so a go.mod file that was already tidy stays byte for
// byte identical across a tidy.
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
// where the rest of them go: the main module's own branch, and the dependency's
// default branch after that.
//
// The marker is read from the line the version lives on, so a fork consumed
// through a replace carries it on the replace line.
//
//	require github.com/wow-look-at-my/foo v0.0.0 // branch=v1
//	require github.com/wow-look-at-my/bar v0.0.0 // indirect; branch=v1
//
// The two go-toolchain spellings that carried this before are read as well, so
// a go.mod file that records one resolves the way it always did.
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
// org module, and returns m unchanged for every other module. It is how a
// writer asks the question, so that go.mod and vendor/modules.txt name the
// same placeholder for the same module.
func PlaceholderModule(m module.Version) module.Version {
	if !IsOrg(m.Path) {
		return m
	}
	m.Version = Placeholder(m.Path)
	return m
}
