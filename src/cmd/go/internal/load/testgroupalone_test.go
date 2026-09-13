// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"os"
	"path/filepath"
	"testing"
)

// memberFor writes one test file and wraps it as a member, so a case reads as
// the source that produced it.
func memberFor(test *testing.T, source string, imports ...string) TestGroupMember {
	test.Helper()

	dir := test.TempDir()
	name := "x_test.go"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o666); err != nil {
		test.Fatal(err)
	}
	pkg := &Package{}
	pkg.Dir = dir
	pkg.TestGoFiles = []string{name}
	pkg.TestImports = imports
	return TestGroupMember{Package: pkg, WithTests: pkg}
}

// compress/bzip2 is the package that found this. Its test file holds
// `var e = mustLoadFile("testdata/e.txt.bz2")`, and in a shared binary that
// ran at every member's start, in every member's directory.
func TestAPackageThatReadsItsOwnFilesAsItInitializesTravelsAlone(test *testing.T) {
	bzip2 := memberFor(test, `package bzip2

import "os"

func mustLoadFile(name string) []byte {
	b, err := os.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}

var e = mustLoadFile("testdata/e.txt.bz2")
`, "os")

	if !travelsAlone(bzip2) {
		test.Error("a package whose initializer opens its own file must not share a binary")
	}
}

// The real file, not a copy of its shape. compress/bzip2 declares its loads
// inside a `var (...)` block, which a check that only reads single-var lines
// misses, and that whole group then panics on every start but bzip2's own.
func TestTheRealBzip2TestFileIsCaught(test *testing.T) {
	dir := filepath.Join(testGOROOT(test), "src", "compress", "bzip2")
	if _, err := os.Stat(filepath.Join(dir, "bzip2_test.go")); err != nil {
		test.Skipf("no bzip2 source to read: %v", err)
	}

	pkg := &Package{}
	pkg.Dir = dir
	pkg.TestGoFiles = []string{"bzip2_test.go"}
	pkg.TestImports = []string{"os"}

	if !travelsAlone(TestGroupMember{Package: pkg, WithTests: pkg}) {
		test.Error("compress/bzip2 reads its own testdata as it initializes and must travel alone")
	}
}

// testGOROOT answers the tree this test is running out of.
func testGOROOT(test *testing.T) string {
	test.Helper()

	dir, err := os.Getwd()
	if err != nil {
		test.Fatal(err)
	}
	// .../src/cmd/go/internal/load -> the tree root.
	for depth := 0; depth < 5; depth++ {
		dir = filepath.Dir(dir)
	}
	return dir
}

func TestAnInitFunctionCountsTheSameWay(test *testing.T) {
	member := memberFor(test, `package p

import "os"

func init() { os.ReadFile("testdata/x") }
`, "os")

	if !travelsAlone(member) {
		test.Error("an init function runs at every start too")
	}
}

// A package that only builds values travels with others. Excluding it would
// cost a binary and buy nothing.
func TestAPackageThatOnlyBuildsValuesStillTravels(test *testing.T) {
	for _, row := range []struct {
		name    string
		source  string
		imports []string
	}{
		{
			"a literal, and no file package named",
			"package p\n\nvar limit = 10\n",
			nil,
		},
		{
			"a call, but nothing that reads the working directory",
			"package p\n\nimport \"regexp\"\n\nvar re = regexp.MustCompile(`a`)\n",
			[]string{"regexp"},
		},
		{
			"os named, but nothing runs as the package initializes",
			"package p\n\nimport \"os\"\n\nvar name = os.DevNull\n",
			[]string{"os"},
		},
	} {
		test.Run(row.name, func(test *testing.T) {
			if travelsAlone(memberFor(test, row.source, row.imports...)) {
				test.Error("this package costs a binary for no reason")
			}
		})
	}
}

// An unreadable file says nothing about its initializers, and the group is
// what is at risk.
func TestAFileThatWillNotParseKeepsItsPackageOut(test *testing.T) {
	member := memberFor(test, "package p\n\nimport \"os\"\n\nvar x = os.(((\n", "os")

	if !travelsAlone(member) {
		test.Error("a file this cannot read must not be assumed harmless")
	}
}

// The partition has to act on the answer, not merely compute it.
func TestGroupMembersKeepsALoneMemberApart(test *testing.T) {
	reader := memberFor(test, `package a

import "os"

var data = os.Getenv("HOME") + read()

func read() string { return "" }
`, "os")
	reader.Package.ImportPath = "a"

	plain := memberFor(test, "package b\n\nvar limit = 1\n")
	plain.Package.ImportPath = "b"

	other := memberFor(test, "package c\n\nvar limit = 2\n")
	other.Package.ImportPath = "c"

	groups := GroupMembers([]TestGroupMember{reader, plain, other})

	for _, group := range groups {
		if len(group) == 1 {
			continue
		}
		for _, member := range group {
			if member.Package.ImportPath == "a" {
				test.Errorf("the lone member shares a binary with %d others", len(group)-1)
			}
		}
	}
	if len(groups) < 2 {
		test.Errorf("got %d group(s); the lone member needs its own", len(groups))
	}
}
