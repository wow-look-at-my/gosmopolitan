// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"context"
	"go/build"
	"sort"

	"cmd/go/internal/modload"
	"cmd/go/internal/str"
	"cmd/go/internal/trace"
)

// TestGroupMember is one package inside a shared test binary, with the test
// variants TestPackagesAndErrors already built for it.
type TestGroupMember struct {
	Package *Package
	WithTests   *Package
	ExtTests  *Package
}

// TestGroupMain builds ONE main package holding the tests of several packages.
//
// The go command still starts the binary once per package and says which one
// with -test.unit, so every per-package result, output and cache entry stays
// what it was. What the packages share is the compile of the generated main and
// the link. A binary per package pays both over and over, and a wasm runtime
// pays a whole module compile for each one.
//
// A member's test variant keeps the import path of the package it tests, so a
// group can only hold packages that do not reach each other. Two packages at
// one path cannot sit in one link. The plain copy of B that another member's
// tests import is exactly such a second copy.
func TestGroupMain(ld *modload.Loader, ctx context.Context, opts PackageOpts, members []TestGroupMember, cover *TestCover, name string) *Package {
	ctx, span := trace.StartSpan(ctx, "load.TestGroupMain")
	defer span.Done()

	pre := newPreload()
	defer pre.flush()

	var stk ImportStack
	stk.Push(ImportInfo{Pkg: "testmain"})

	first := members[0].Package
	ldflags := append(first.Internal.Ldflags, "-X", "testing.testBinary=1")
	gccgoflags := append(first.Internal.Gccgoflags, "-Wl,--defsym,testing.gccgoTestBinary=1")

	testMain := &Package{
		PackagePublic: PackagePublic{
			Name:       "main",
			Dir:        first.Dir,
			GoFiles:    []string{"_testmain.go"},
			ImportPath: name,
			Root:       first.Root,
			Imports:    str.StringList(TestMainDeps),
			Module:     first.Module,
		},
		Internal: PackageInternal{
			Build:          &build.Package{Name: "main"},
			BuildInfo:      first.Internal.BuildInfo,
			Asmflags:       first.Internal.Asmflags,
			Gcflags:        first.Internal.Gcflags,
			Ldflags:        ldflags,
			Gccgoflags:     gccgoflags,
			OrigImportPath: first.Internal.OrigImportPath,
			PGOProfile:     first.Internal.PGOProfile,
		},
	}
	firstBuild := first.Internal.Build
	testMain.DefaultGODEBUG = defaultGODEBUG(ld, testMain, firstBuild.Directives, firstBuild.TestDirectives, firstBuild.XTestDirectives)

	deps := str.StringList(TestMainDeps)
	if cover != nil {
		deps = append(deps, "internal/coverage/cfile")
	}
	ldDeps, err := LinkerDeps(ld, first)
	if err != nil && testMain.Error == nil {
		testMain.Error = &PackageError{Err: err}
	}
	deps = append(deps, ldDeps...)
	for _, dep := range deps {
		imported, err := loadImport(ld, ctx, opts, pre, dep, "", nil, &stk, nil, 0)
		if err != nil && testMain.Error == nil {
			testMain.Error = err
			testMain.Incomplete = true
		}
		testMain.Internal.Imports = append(testMain.Internal.Imports, imported)
	}

	units := make([]testUnit, 0, len(members))
	for idx, member := range members {
		funcs, err := loadTestFuncs(member.Package)
		if err != nil && testMain.Error == nil {
			testMain.setLoadPackageDataError(err, member.Package.ImportPath, &stk, nil)
		}
		funcs.Cover = cover

		if member.WithTests != nil && len(member.WithTests.GoFiles)+len(member.WithTests.CgoFiles) > 0 {
			testMain.Internal.Imports = append(testMain.Internal.Imports, member.WithTests)
			testMain.Imports = append(testMain.Imports, member.WithTests.ImportPath)
			funcs.ImportTest = true
		}
		if member.ExtTests != nil {
			testMain.Internal.Imports = append(testMain.Internal.Imports, member.ExtTests)
			testMain.Imports = append(testMain.Imports, member.ExtTests.ImportPath)
			funcs.ImportXtest = true
		}

		unit := funcs.Units()[0]
		unit.Alias = testAlias(idx, false)
		unit.XAlias = testAlias(idx, true)
		for pos := range funcs.Tests {
			funcs.Tests[pos].Package = aliasFor(funcs.Tests[pos].Package, idx)
		}
		for pos := range funcs.Benchmarks {
			funcs.Benchmarks[pos].Package = aliasFor(funcs.Benchmarks[pos].Package, idx)
		}
		for pos := range funcs.FuzzTargets {
			funcs.FuzzTargets[pos].Package = aliasFor(funcs.FuzzTargets[pos].Package, idx)
		}
		for pos := range funcs.Examples {
			funcs.Examples[pos].Package = aliasFor(funcs.Examples[pos].Package, idx)
		}
		if funcs.TestMain != nil {
			funcs.TestMain.Package = aliasFor(funcs.TestMain.Package, idx)
		}
		unit.Tests = funcs.Tests
		unit.Benchmarks = funcs.Benchmarks
		unit.FuzzTargets = funcs.FuzzTargets
		unit.Examples = funcs.Examples
		unit.TestMain = funcs.TestMain
		unit.ImportTest = funcs.ImportTest
		unit.ImportXtest = funcs.ImportXtest
		units = append(units, unit)
	}
	stk.Pop()

	// Only matters for go list -test output.
	sort.Strings(testMain.Imports)
	kept := 0
	for _, path := range testMain.Imports {
		if kept == 0 || path != testMain.Imports[kept-1] {
			testMain.Imports[kept] = path
			kept++
		}
	}
	testMain.Imports = testMain.Imports[:kept]
	testMain.Internal.RawImports = str.StringList(testMain.Imports)

	// Each member's own dependencies must reach its test copies, and a member
	// the group holds is never in another member's closure, so the rewrites
	// cannot collide.
	for _, member := range members {
		if cycleErr := recompileForTest(testMain, member.Package, member.WithTests, member.ExtTests); cycleErr != nil {
			member.WithTests.Error = cycleErr
			member.WithTests.Incomplete = true
		}
	}

	if !opts.SuppressBuildInfo {
		testMain.setBuildInfo(ctx, ld.Fetcher(), opts.AutoVCS)
	}

	if cover != nil {
		testMain.Internal.Cover.Mode = "testmain"
		for _, member := range members {
			if member.WithTests == nil {
				continue
			}
			member.WithTests.Internal.Cover.Mode = member.Package.Internal.Cover.Mode
			if cover.Local {
				member.WithTests.Internal.Cover.Mode = cover.Mode
			}
		}
	}

	content, err := renderTestmain(testMainData{Units: units, Cover: cover})
	if err != nil && testMain.Error == nil {
		testMain.Error = &PackageError{Err: err}
		testMain.Incomplete = true
	}
	testMain.Internal.TestmainGo = &content
	return testMain
}

// GroupMembers partitions packages into the groups that may share one binary.
//
// A member's test variant occupies the import path of the package it tests. So
// two members may travel together only when neither one's tests reach the
// other: reaching it would put a second package at that same path in the same
// link. Every package still runs, and every one still reports on its own. What
// changes is how many binaries the whole set costs.
func GroupMembers(members []TestGroupMember) [][]TestGroupMember {
	reaches := make([]map[string]bool, len(members))
	for idx, member := range members {
		var roots []*Package
		if member.WithTests != nil {
			roots = append(roots, member.WithTests)
		}
		if member.ExtTests != nil {
			roots = append(roots, member.ExtTests)
		}
		seen := map[string]bool{}
		for _, reached := range PackageList(roots) {
			seen[reached.ImportPath] = true
		}
		reaches[idx] = seen
	}

	var groups [][]int
	for idx, member := range members {
		placed := false
		for pos, group := range groups {
			fits := true
			for _, other := range group {
				if reaches[idx][members[other].Package.ImportPath] || reaches[other][member.Package.ImportPath] {
					fits = false
					break
				}
			}
			if fits {
				groups[pos] = append(group, idx)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []int{idx})
		}
	}

	out := make([][]TestGroupMember, 0, len(groups))
	for _, group := range groups {
		batch := make([]TestGroupMember, 0, len(group))
		for _, idx := range group {
			batch = append(batch, members[idx])
		}
		out = append(out, batch)
	}
	return out
}

// aliasFor answers the import name a test function is reached through once its
// package is one unit among several. loadTestFuncs writes the name a single
// package uses, and every unit past the first needs its own.
func aliasFor(name string, idx int) string {
	return testAlias(idx, name == testAlias(0, true))
}
