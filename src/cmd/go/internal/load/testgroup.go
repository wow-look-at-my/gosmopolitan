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
	Ptest   *Package
	Pxtest  *Package
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

	pmain := &Package{
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
	pmain.DefaultGODEBUG = defaultGODEBUG(ld, pmain, firstBuild.Directives, firstBuild.TestDirectives, firstBuild.XTestDirectives)

	deps := str.StringList(TestMainDeps)
	if cover != nil {
		deps = append(deps, "internal/coverage/cfile")
	}
	ldDeps, err := LinkerDeps(ld, first)
	if err != nil && pmain.Error == nil {
		pmain.Error = &PackageError{Err: err}
	}
	deps = append(deps, ldDeps...)
	for _, dep := range deps {
		imported, err := loadImport(ld, ctx, opts, pre, dep, "", nil, &stk, nil, 0)
		if err != nil && pmain.Error == nil {
			pmain.Error = err
			pmain.Incomplete = true
		}
		pmain.Internal.Imports = append(pmain.Internal.Imports, imported)
	}

	units := make([]testUnit, 0, len(members))
	for idx, member := range members {
		funcs, err := loadTestFuncs(member.Package)
		if err != nil && pmain.Error == nil {
			pmain.setLoadPackageDataError(err, member.Package.ImportPath, &stk, nil)
		}
		funcs.Cover = cover

		if member.Ptest != nil && len(member.Ptest.GoFiles)+len(member.Ptest.CgoFiles) > 0 {
			pmain.Internal.Imports = append(pmain.Internal.Imports, member.Ptest)
			pmain.Imports = append(pmain.Imports, member.Ptest.ImportPath)
			funcs.ImportTest = true
		}
		if member.Pxtest != nil {
			pmain.Internal.Imports = append(pmain.Internal.Imports, member.Pxtest)
			pmain.Imports = append(pmain.Imports, member.Pxtest.ImportPath)
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
	sort.Strings(pmain.Imports)
	kept := 0
	for _, path := range pmain.Imports {
		if kept == 0 || path != pmain.Imports[kept-1] {
			pmain.Imports[kept] = path
			kept++
		}
	}
	pmain.Imports = pmain.Imports[:kept]
	pmain.Internal.RawImports = str.StringList(pmain.Imports)

	// Each member's own dependencies must reach its test copies, and a member
	// the group holds is never in another member's closure, so the rewrites
	// cannot collide.
	for _, member := range members {
		if cycleErr := recompileForTest(pmain, member.Package, member.Ptest, member.Pxtest); cycleErr != nil {
			member.Ptest.Error = cycleErr
			member.Ptest.Incomplete = true
		}
	}

	if !opts.SuppressBuildInfo {
		pmain.setBuildInfo(ctx, ld.Fetcher(), opts.AutoVCS)
	}

	if cover != nil {
		pmain.Internal.Cover.Mode = "testmain"
		for _, member := range members {
			if member.Ptest == nil {
				continue
			}
			member.Ptest.Internal.Cover.Mode = member.Package.Internal.Cover.Mode
			if cover.Local {
				member.Ptest.Internal.Cover.Mode = cover.Mode
			}
		}
	}

	content, err := renderTestmain(testMainData{Units: units, Cover: cover})
	if err != nil && pmain.Error == nil {
		pmain.Error = &PackageError{Err: err}
		pmain.Incomplete = true
	}
	pmain.Internal.TestmainGo = &content
	return pmain
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
		if member.Ptest != nil {
			roots = append(roots, member.Ptest)
		}
		if member.Pxtest != nil {
			roots = append(roots, member.Pxtest)
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
