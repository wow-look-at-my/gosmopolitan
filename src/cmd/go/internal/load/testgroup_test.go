// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import "testing"

// build_pgo_auto_multi is the case that found this: -pgo=auto gives two main
// packages their own default.pgo, and every dependency is compiled once per
// profile. One binary cannot link both copies.
func TestGroupMembersKeepsProfilesApart(test *testing.T) {
	withProfile := func(path, profile string) TestGroupMember {
		member := memberFor(test, "package p\n\nvar limit = 1\n")
		member.Package.ImportPath = path
		member.Package.Internal.PGOProfile = profile
		return member
	}
	first := withProfile("a", "/src/a/default.pgo")
	second := withProfile("b", "/src/b/default.pgo")
	none := withProfile("nopgo", "")
	same := withProfile("c", "/src/a/default.pgo")

	groups := GroupMembers([]TestGroupMember{first, second, none, same})

	for _, group := range groups {
		profile := group[0].Package.Internal.PGOProfile
		for _, member := range group[1:] {
			if member.Package.Internal.PGOProfile != profile {
				test.Errorf("%s (%q) shares a binary with %s (%q)",
					member.Package.ImportPath, member.Package.Internal.PGOProfile,
					group[0].Package.ImportPath, profile)
			}
		}
	}
	if len(groups) != 3 {
		test.Errorf("got %d groups, want 3: one per profile, a and c together", len(groups))
	}
}
