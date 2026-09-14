// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import "testing"

// member wraps a package as a group member whose tests are the package itself.
func member(path, profile string) TestGroupMember {
	pkg := &Package{}
	pkg.ImportPath = path
	pkg.Internal.PGOProfile = profile
	return TestGroupMember{Package: pkg, WithTests: pkg}
}

// Every package shares one binary, however the packages import each other.
func TestGroupMembersSharesOneBinary(test *testing.T) {
	groups := GroupMembers([]TestGroupMember{member("a", ""), member("b", ""), member("c", "")})
	if len(groups) != 1 || len(groups[0]) != 3 {
		test.Fatalf("got %d groups, want one holding all 3", len(groups))
	}
}

// build_pgo_auto_multi is the case that found this: -pgo=auto gives two main
// packages their own default.pgo, and every dependency is compiled once per
// profile. One binary cannot link both copies.
func TestGroupMembersKeepsProfilesApart(test *testing.T) {
	groups := GroupMembers([]TestGroupMember{
		member("a", "/src/a/default.pgo"),
		member("b", "/src/b/default.pgo"),
		member("nopgo", ""),
		member("c", "/src/a/default.pgo"),
	})

	for _, group := range groups {
		profile := group[0].Package.Internal.PGOProfile
		for _, other := range group[1:] {
			if other.Package.Internal.PGOProfile != profile {
				test.Errorf("%s (%q) shares a binary with %s (%q)",
					other.Package.ImportPath, other.Package.Internal.PGOProfile,
					group[0].Package.ImportPath, profile)
			}
		}
	}
	if len(groups) != 3 {
		test.Errorf("got %d groups, want 3: one per profile, a and c together", len(groups))
	}
}

// The linker writes one default GODEBUG into a binary, and a package's
// //go:debug lines decide its own.
func TestGroupMembersKeepsGODEBUGApart(test *testing.T) {
	plain := member("a", "")
	panicnil := member("b", "")
	panicnil.GODEBUG = "panicnil=1"
	same := member("c", "")

	groups := GroupMembers([]TestGroupMember{plain, panicnil, same})
	if len(groups) != 2 {
		test.Fatalf("got %d groups, want 2: b alone, a and c together", len(groups))
	}
	for _, group := range groups {
		for _, other := range group[1:] {
			if other.GODEBUG != group[0].GODEBUG {
				test.Errorf("%s (%q) shares a binary with %s (%q)", other.Package.ImportPath, other.GODEBUG, group[0].Package.ImportPath, group[0].GODEBUG)
			}
		}
	}
}
