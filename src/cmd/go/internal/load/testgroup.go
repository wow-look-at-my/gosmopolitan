// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"go/build"
	"internal/godebugs"
	"slices"
	"sort"
	"strings"

	"cmd/go/internal/cfg"
	"cmd/go/internal/modload"
	"cmd/go/internal/str"
	"cmd/go/internal/trace"
	"cmd/internal/objabi"
)

// testUnitBarriers name the symbols a walk of the binary must not follow when
// it asks what one package's tests reach. Each of them names every package in
// the binary, so a walk through one reaches them all and its content moves
// whenever any of them does.
var testUnitBarriers = []string{"main.units", "go:testinittasks", "runtime.testinittasks"}

// testUnitRoots names the symbols one package's tests start from: the
// initialization its test files deferred, and every function the generated
// main can call for it. The linker walks these to decide what code the
// package's tests reach, which is what its cached result is keyed on.
func testUnitRoots(unit testUnit) []string {
	prefix := objabi.PathToPrefix(unit.UnitID) + "."
	xprefix := objabi.PathToPrefix(unit.UnitID+"_test") + "."
	symbol := func(entry testFunc) string {
		if entry.Package == unit.XAlias {
			return xprefix + entry.Name
		}
		return prefix + entry.Name
	}
	roots := []string{prefix + ".inittask.test", prefix + ".inittask.xtest"}
	for _, entry := range unit.Tests {
		roots = append(roots, symbol(entry))
	}
	for _, entry := range unit.Benchmarks {
		roots = append(roots, symbol(entry))
	}
	for _, entry := range unit.FuzzTargets {
		roots = append(roots, symbol(entry))
	}
	for _, entry := range unit.Examples {
		roots = append(roots, symbol(entry))
	}
	if unit.TestMain != nil {
		roots = append(roots, symbol(*unit.TestMain))
	}
	return roots
}

// testUnitSpec writes what the linker's -testunits flag reads.
func testUnitSpec(units []testUnit) string {
	var spec strings.Builder
	for _, name := range testUnitBarriers {
		spec.WriteString("barrier " + name + "\n")
	}
	for _, unit := range units {
		spec.WriteString("unit " + unit.UnitID + "\n")
		for _, root := range testUnitRoots(unit) {
			spec.WriteString("root " + root + "\n")
		}
	}
	return spec.String()
}

// TestGroupMember is one package inside a shared test binary, with the test
// variants TestPackagesAndErrors already built for it.
type TestGroupMember struct {
	Package   *Package
	WithTests *Package
	ExtTests  *Package

	// GODEBUG is the default GODEBUG this package's test binary would get
	// on its own, from the main module and the package's //go:debug lines.
	GODEBUG string

	// Binary is the file name, without an executable suffix, of this
	// package's copy of the binary under go test -c.
	Binary string
}

// TestGODEBUG answers the default GODEBUG of the test binary of pkg alone.
func TestGODEBUG(ld *modload.Loader, pkg *Package) string {
	main := &Package{
		PackagePublic: PackagePublic{Name: "main", Module: pkg.Module},
		Internal:      PackageInternal{Build: &build.Package{Name: "main"}},
	}
	pkgBuild := pkg.Internal.Build
	return defaultGODEBUG(ld, main, pkgBuild.Directives, pkgBuild.TestDirectives, pkgBuild.XTestDirectives)
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

// A test result depends on the code that runs it, and that code is generated
// here rather than compiled from any package's sources. A cache key without it
// serves results the running binary never produced.
//
// The generated main's own compile cannot supply this, because it holds every
// package in the binary. Rendering this one alone names the template and this
// package's tests, and says nothing about a sibling.
func unitDigest(unit testUnit, cover *TestCover) (string, error) {
	alone := unit
	alone.Alias = testAlias(0, false)
	alone.XAlias = testAlias(0, true)
	alone.Tests = realiasFuncs(unit.Tests, unit.Alias, unit.XAlias)
	alone.Benchmarks = realiasFuncs(unit.Benchmarks, unit.Alias, unit.XAlias)
	alone.FuzzTargets = realiasFuncs(unit.FuzzTargets, unit.Alias, unit.XAlias)
	alone.Examples = realiasFuncs(unit.Examples, unit.Alias, unit.XAlias)
	if unit.TestMain != nil {
		only := realiasFuncs([]testFunc{*unit.TestMain}, unit.Alias, unit.XAlias)
		alone.TestMain = &only[0]
	}
	rendered, err := renderTestmain(testMainData{Units: []testUnit{alone}, Cover: cover})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(rendered)
	return hex.EncodeToString(sum[:]), nil
}

// realiasFuncs rewrites each function's import name as if its package were the
// only one in the binary, so a digest does not move when a package's position
// in the group moves.
func realiasFuncs(funcs []testFunc, alias, xalias string) []testFunc {
	out := make([]testFunc, len(funcs))
	copy(out, funcs)
	for idx := range out {
		switch out[idx].Package {
		case alias:
			out[idx].Package = testAlias(0, false)
		case xalias:
			out[idx].Package = testAlias(0, true)
		}
	}
	return out
}

// darwinTextSegprot is the host linker flag that gives a test binary's __TEXT
// segment the protections the internal linker gives it, rwx at most and r-x
// to start, so a test reading them sees one answer whichever linker its
// binary needed. Every test binary carries it, so a package's link settings,
// and the key of its cached results, do not depend on what shares its binary.
const darwinTextSegprot = "-Wl,-segprot,__TEXT,rwx,rx"

// withExtldflag adds one host linker flag to ldflags. The linker keeps only
// the last -extldflags, so the flag joins the value ldflags already carry.
func withExtldflag(ldflags []string, flag string) []string {
	out := slices.Clone(ldflags)
	for idx := len(out) - 1; idx >= 0; idx-- {
		arg := out[idx]
		if (arg == "-extldflags" || arg == "--extldflags") && idx+1 < len(out) {
			out[idx+1] += " " + flag
			return out
		}
		if strings.HasPrefix(arg, "-extldflags=") || strings.HasPrefix(arg, "--extldflags=") {
			out[idx] += " " + flag
			return out
		}
	}
	return append(out, "-extldflags="+flag)
}

func TestGroupMain(ld *modload.Loader, ctx context.Context, opts PackageOpts, members []TestGroupMember, cover *TestCover, name string) (*Package, map[string]string) {
	ctx, span := trace.StartSpan(ctx, "load.TestGroupMain")
	defer span.Done()

	pre := newPreload()
	defer pre.flush()

	var stk ImportStack
	stk.Push(ImportInfo{Pkg: "testmain"})

	first := members[0].Package
	ldflags := append(slices.Clip(first.Internal.Ldflags), "-X", "testing.testBinary=1")
	if cfg.Goos == "darwin" {
		ldflags = withExtldflag(ldflags, darwinTextSegprot)
	}
	gccgoflags := append(first.Internal.Gccgoflags,
		"-Wl,--defsym,testing.gccgoTestBinary=1")

	testMain := &Package{
		PackagePublic: PackagePublic{
			Name:       "main",
			Dir:        first.Dir,
			GoFiles:    []string{"_testmain.go"},
			ImportPath: name,
			Root:       first.Root,
			Imports:    groupedMainDeps(len(members)),
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

	deps := groupedMainDeps(len(members))
	if cover != nil {
		deps = append(deps, "internal/coverage/cfile")
	}
	// Program startup initializes what the generated main itself runs on.
	// Each member, and whatever only members import, is initialized when
	// the binary is started for that member's tests, the way a binary of
	// its own would be.
	if len(members) > 1 {
		testMain.Internal.TestStartup = str.StringList(deps)
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
		unit.GODEBUG = member.GODEBUG
		unit.Binary = member.Binary
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

	// A package's tests compile the same way whether their binary holds one
	// package or many, so a result cached from one serves the other.
	for _, member := range members {
		shareMember(member)
	}
	if profile := first.Internal.PGOProfile; profile != "" {
		attachProfile(testMain, profile, first.ImportPath)
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
			// The variant is linked in place of the package and keeps its
			// symbols, so both are instrumented alike. Every package that
			// imports it then inlines instrumented code, as it would
			// compiled against the variant. A run reports coverage only of
			// the packages it selects, so another package's run is unchanged.
			if member.WithTests.Internal.TestVariantOf != nil {
				member.Package.Internal.Cover.Mode = member.WithTests.Internal.Cover.Mode
			}
		}
	}

	data := &testMainData{Units: units, Cover: cover, Grouped: len(units) > 1}
	content, err := renderTestmain(*data)
	if err != nil && testMain.Error == nil {
		testMain.Error = &PackageError{Err: err}
		testMain.Incomplete = true
	}
	testMain.Internal.TestmainGo = &content
	testMain.Internal.testmainData = data
	testMain.Internal.TestUnitSpec = testUnitSpec(units)

	// Key by UnitID, never by ImportPath. ImportPath is what testdeps reports,
	// and it is EMPTY for command-line-arguments and for a package outside a
	// module. The caller reads this map by the package's real path, so an empty
	// key hands it no digest and the test cache aborts the build.
	digests := make(map[string]string, len(units))
	for _, unit := range units {
		digest, err := unitDigest(unit, cover)
		if err != nil && testMain.Error == nil {
			testMain.Error = &PackageError{Err: err}
			testMain.Incomplete = true
		}
		digests[unit.UnitID] = digest
	}
	return testMain, digests
}

// GroupMembers partitions packages into the binaries their tests share: one
// per PGO profile and per value of the GODEBUG settings a program reads only
// as it starts. A profile is compiled into every package a binary links, the
// runtime included. A binary applies the rest of a package's default GODEBUG
// when it is started for that package. Every package still runs, and every one
// still reports on its own.
func GroupMembers(members []TestGroupMember) [][]TestGroupMember {
	var out [][]TestGroupMember
	byBinary := map[[2]string]int{}
	for _, member := range members {
		key := [2]string{member.Package.Internal.PGOProfile, startupGODEBUG(member.GODEBUG)}
		pos, found := byBinary[key]
		if !found {
			pos = len(out)
			byBinary[key] = pos
			out = append(out, nil)
		}
		out[pos] = append(out[pos], member)
	}
	return out
}

// attachProfile compiles every package in testMain's binary with the PGO
// profile its members share, the way a main package's own dependencies are.
// A package only the tests import was loaded without one, so it gets a copy
// that carries the profile, and each importer is pointed at the copy.
func attachProfile(testMain *Package, profile, forTest string) {
	copies := map[*Package]*Package{}
	for _, pkg := range PackageList([]*Package{testMain}) {
		if pkg != testMain && pkg.Internal.PGOProfile == "" {
			pkgCopy := new(Package)
			*pkgCopy = *pkg
			pkgCopy.ForTest = forTest
			pkgCopy.Target = ""
			pkgCopy.Internal.BuildInfo = nil
			pkgCopy.Internal.PGOProfile = profile
			copies[pkg] = pkgCopy
		}
	}
	if len(copies) == 0 {
		return
	}
	rewire := func(pkg *Package) {
		imports := slices.Clone(pkg.Internal.Imports)
		changed := false
		for idx, imp := range imports {
			if pkgCopy := copies[imp]; pkgCopy != nil {
				imports[idx] = pkgCopy
				changed = true
			}
		}
		if changed {
			pkg.Internal.Imports = imports
		}
	}
	// A package without the profile may be in other binaries too, so only
	// its copy is rewired.
	for _, pkg := range PackageList([]*Package{testMain}) {
		if pkgCopy := copies[pkg]; pkgCopy != nil {
			rewire(pkgCopy)
			continue
		}
		rewire(pkg)
	}
}

// startupGODEBUG answers the part of a default GODEBUG that only the start of
// the program reads: the settings internal/godebugs marks immutable. Changing
// the default later, when a binary is started for one of its packages, reaches
// every other setting.
func startupGODEBUG(godebug string) string {
	var kept []string
	for _, setting := range strings.Split(godebug, ",") {
		name, _, _ := strings.Cut(setting, "=")
		if info := godebugs.Lookup(name); info != nil && info.Immutable {
			kept = append(kept, setting)
		}
	}
	return strings.Join(kept, ",")
}

// shareMember prepares one package of a binary holding several packages'
// tests. Nothing is recompiled against its test variants. The variant with
// the package's own test files is linked in place of the package, and keeps
// the symbol indices every other package refers to it by (-testvariant).
// Both variants defer what their _test.go files initialize until the tests
// of this package run (-testinit), so neither touches another package's run.
func shareMember(member TestGroupMember) {
	pkg := member.Package
	if withTests := member.WithTests; withTests != nil && withTests != pkg {
		withTests.Internal.TestInit = pkg.ImportPath
		// Only a package something else can import is replaced. A command
		// is never imported, and neither is a package whose only Go files
		// are tests.
		if pkg.Name != "main" && pkg.Error == nil && len(pkg.GoFiles)+len(pkg.CgoFiles) > 0 {
			withTests.Internal.TestVariantOf = pkg
		}
	}
	if member.ExtTests != nil {
		member.ExtTests.Internal.TestInit = pkg.ImportPath
	}
}

// TestCycle reports the import cycle a package's internal test files would
// close, if they import a package that imports the package itself.
func TestCycle(pkg, withTests *Package) *PackageError {
	if withTests == nil || withTests == pkg {
		return nil
	}
	return testImportCycle(withTests, pkg)
}

// aliasFor answers the import name a test function is reached through once its
// package is one unit among several. loadTestFuncs writes the name a single
// package uses, and every unit past the first needs its own.
func aliasFor(name string, idx int) string {
	return testAlias(idx, name == testAlias(0, true))
}
