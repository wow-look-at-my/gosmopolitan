// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"slices"
	"testing"
)

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

// A binary applies a package's default GODEBUG when it is started for that
// package, so a setting the program can change as it runs does not split
// packages apart.
func TestGroupMembersShareAcrossChangeableGODEBUG(test *testing.T) {
	plain := member("a", "")
	panicnil := member("b", "")
	panicnil.GODEBUG = "panicnil=1,httplaxcontentlength=1"
	same := member("c", "")

	groups := GroupMembers([]TestGroupMember{plain, panicnil, same})
	if len(groups) != 1 || len(groups[0]) != 3 {
		test.Fatalf("got %d groups, want one holding all 3", len(groups))
	}
}

// A setting read only as the program starts keeps the value the binary
// started with, so a package that needs another value gets its own binary.
func TestGroupMembersKeepsStartupGODEBUGApart(test *testing.T) {
	plain := member("a", "")
	maxprocs := member("b", "")
	maxprocs.GODEBUG = "panicnil=1,updatemaxprocs=0"
	same := member("c", "")
	same.GODEBUG = "panicnil=1"

	groups := GroupMembers([]TestGroupMember{plain, maxprocs, same})
	if len(groups) != 2 {
		test.Fatalf("got %d groups, want 2: b alone, a and c together", len(groups))
	}
	for _, group := range groups {
		for _, other := range group[1:] {
			if other.Package.ImportPath == "b" || group[0].Package.ImportPath == "b" {
				test.Errorf("%s shares a binary with %s", other.Package.ImportPath, group[0].Package.ImportPath)
			}
		}
	}
}

// The linker keeps only the last -extldflags, so a host linker flag the go
// command adds joins the one the user gave instead of replacing it.
func TestWithExtldflagJoinsTheLastValue(test *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{nil, []string{"-extldflags=-X"}},
		{[]string{"-s"}, []string{"-s", "-extldflags=-X"}},
		{[]string{"-extldflags=-static"}, []string{"-extldflags=-static -X"}},
		{[]string{"-extldflags", "-static"}, []string{"-extldflags", "-static -X"}},
		{[]string{"-extldflags=-a", "--extldflags=-b"}, []string{"-extldflags=-a", "--extldflags=-b -X"}},
	}
	for _, tcase := range cases {
		in := slices.Clone(tcase.in)
		got := withExtldflag(tcase.in, "-X")
		if !slices.Equal(got, tcase.want) {
			test.Errorf("withExtldflag(%q) = %q, want %q", tcase.in, got, tcase.want)
		}
		if !slices.Equal(tcase.in, in) {
			test.Errorf("withExtldflag changed its argument to %q", tcase.in)
		}
	}
}
