// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/token"
	"internal/lazytemplate"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"cmd/go/internal/fsys"
	"cmd/go/internal/modload"
	"cmd/go/internal/str"
	"cmd/go/internal/trace"
)

// TestMainDeps is what the generated main imports. Keep it to what upstream
// carries: this list IS what `go list` reports as a test binary's imports, so
// a package added here shows up in every one of them. A main that holds
// several packages needs fmt as well, and asks for it through groupedMainDeps.
var TestMainDeps = []string{
	// Dependencies for testmain.
	"os",
	"reflect",
	"testing",
	"testing/internal/testdeps",
}

// groupedMainDeps is TestMainDeps plus what a main holding several packages
// needs on top: fmt, to name the package a caller asked for and did not find.
func groupedMainDeps(units int) []string {
	deps := str.StringList(TestMainDeps)
	if units > 1 {
		deps = append(deps, "fmt")
		sort.Strings(deps)
	}
	return deps
}

type TestCover struct {
	Mode  string
	Local bool
	Pkgs  []*Package
	Paths []string
}

// TestPackagesFor is like TestPackagesAndErrors but it returns
// the package containing an error if the test packages or
// their dependencies have errors.
// Only test packages without errors are returned.
func TestPackagesFor(ld *modload.Loader, ctx context.Context, opts PackageOpts, p *Package, cover *TestCover) (testMain, withTests, extTests, perr *Package) {
	testMain, withTests, extTests = TestPackagesAndErrors(ld, ctx, nil, opts, p, cover)
	for _, p1 := range []*Package{withTests, extTests, testMain} {
		if p1 == nil {
			// extTests may be nil
			continue
		}
		if p1.Error != nil {
			perr = p1
			break
		}
		if p1.Incomplete {
			ps := PackageList([]*Package{p1})
			for _, p := range ps {
				if p.Error != nil {
					perr = p
					break
				}
			}
			break
		}
	}
	if testMain.Error != nil || testMain.Incomplete {
		testMain = nil
	}
	if withTests.Error != nil || withTests.Incomplete {
		withTests = nil
	}
	if extTests != nil && (extTests.Error != nil || extTests.Incomplete) {
		extTests = nil
	}
	return testMain, withTests, extTests, perr
}

// TestPackagesAndErrors returns three packages:
//   - testMain, the package main corresponding to the test binary (running tests in withTests and extTests).
//   - withTests, the package p compiled with added "package p" test files.
//   - extTests, the result of compiling any "package p_test" (external) test files.
//
// If the package has no "package p_test" test files, extTests will be nil.
// If the non-test compilation of package p can be reused
// (for example, if there are no "package p" test files and
// package p need not be instrumented for coverage or any other reason),
// then the returned withTests == p.
//
// If done is non-nil, TestPackagesAndErrors will finish filling out the returned
// package structs in a goroutine and call done once finished. The members of the
// returned packages should not be accessed until done is called.
//
// The caller is expected to have checked that len(p.TestGoFiles)+len(p.XTestGoFiles) > 0,
// or else there's no point in any of this.
func TestPackagesAndErrors(ld *modload.Loader, ctx context.Context, done func(), opts PackageOpts, p *Package, cover *TestCover) (testMain, withTests, extTests *Package) {
	return testPackages(ld, ctx, done, opts, p, cover, false)
}

// TestVariantsFor answers the test copies of p and builds no main for them:
// withTests is p with its in-package test files, extTests its external test package.
//
// A group builds one main for several packages and rewires that main's whole
// dependency graph itself. Building a main here as well would rewire a second
// graph over the same packages, so a caller that groups asks for the variants
// alone.
//
// perr is the package holding the error, as TestPackagesFor answers it: a
// variant's own, or else the first one in the dependencies of a variant that
// is incomplete. An import that fails to load marks the importer incomplete
// and leaves the error on the imported package.
func TestVariantsFor(ld *modload.Loader, ctx context.Context, opts PackageOpts, p *Package, cover *TestCover) (withTests, extTests, perr *Package) {
	_, withTests, extTests = testPackages(ld, ctx, nil, opts, p, cover, true)
	for _, variant := range []*Package{withTests, extTests} {
		if variant == nil {
			continue
		}
		if variant.Error != nil {
			return withTests, extTests, variant
		}
		if !variant.Incomplete {
			continue
		}
		for _, dep := range PackageList([]*Package{variant}) {
			if dep.Error != nil {
				return withTests, extTests, dep
			}
		}
	}
	return withTests, extTests, nil
}

func testPackages(ld *modload.Loader, ctx context.Context, done func(), opts PackageOpts, p *Package, cover *TestCover, variantsOnly bool) (testMain, withTests, extTests *Package) {
	ctx, span := trace.StartSpan(ctx, "load.TestPackagesAndErrors")
	defer span.Done()

	pre := newPreload()
	defer pre.flush()
	allImports := append([]string{}, p.TestImports...)
	allImports = append(allImports, p.XTestImports...)
	pre.preloadImports(ld, ctx, opts, allImports, p.Internal.Build)

	var withTestsErr, extTestsErr *PackageError
	var imports, ximports []*Package
	var stk ImportStack
	var testEmbed, xtestEmbed map[string][]string
	var incomplete bool
	stk.Push(ImportInfo{Pkg: p.ImportPath + " (test)"})
	rawTestImports := str.StringList(p.TestImports)

	for i, path := range p.TestImports {
		p1, err := loadImport(ld, ctx, opts, pre, path, p.Dir, p, &stk, p.Internal.Build.TestImportPos[path], ResolveImport)
		if err != nil && withTestsErr == nil {
			withTestsErr = err
			incomplete = true
		}
		if p1.Incomplete {
			incomplete = true
		}
		p.TestImports[i] = p1.ImportPath
		imports = append(imports, p1)
	}

	var withTestsCompiledImports []string
	if hasSimd := hasSimd(p.TestImports); hasSimd {
		p1, err := loadImport(ld, ctx, opts, pre, SimdBridgePkg, p.Dir, p, &stk, nil, ResolveImport|allowSimdInternalBridge)
		if err != nil && withTestsErr == nil {
			withTestsErr = err
			incomplete = true
		}
		if p1.Incomplete {
			incomplete = true
		}
		imports = append(imports, p1)
		withTestsCompiledImports = append(withTestsCompiledImports, p1.ImportPath)
	}
	var err error
	p.TestEmbedFiles, testEmbed, err = resolveEmbed(p.Dir, p.TestEmbedPatterns)
	if err != nil {
		withTestsErr = &PackageError{
			ImportStack: stk.Copy(),
			Err:         err,
		}
		incomplete = true
		embedErr := err.(*EmbedError)
		withTestsErr.setPos(p.Internal.Build.TestEmbedPatternPos[embedErr.Pattern])
	}
	stk.Pop()

	stk.Push(ImportInfo{Pkg: p.ImportPath + "_test"})
	extTestsNeedsWithTests := false
	var extTestsIncomplete bool
	rawXTestImports := str.StringList(p.XTestImports)

	for i, path := range p.XTestImports {
		p1, err := loadImport(ld, ctx, opts, pre, path, p.Dir, p, &stk, p.Internal.Build.XTestImportPos[path], ResolveImport)
		if err != nil && extTestsErr == nil {
			extTestsErr = err
		}
		if p1.Incomplete {
			extTestsIncomplete = true
		}
		if p1.ImportPath == p.ImportPath {
			extTestsNeedsWithTests = true
		} else {
			ximports = append(ximports, p1)
		}
		p.XTestImports[i] = p1.ImportPath
	}

	var extTestsCompiledImports []string
	if hasSimd := hasSimd(p.XTestImports); hasSimd {
		p1, err := loadImport(ld, ctx, opts, pre, SimdBridgePkg, p.Dir, p, &stk, nil, ResolveImport|allowSimdInternalBridge)
		if err != nil && extTestsErr == nil {
			extTestsErr = err
		}
		if p1.Incomplete {
			extTestsIncomplete = true
		}
		ximports = append(ximports, p1)
		extTestsCompiledImports = append(extTestsCompiledImports, p1.ImportPath)
	}
	p.XTestEmbedFiles, xtestEmbed, err = resolveEmbed(p.Dir, p.XTestEmbedPatterns)
	if err != nil && extTestsErr == nil {
		extTestsErr = &PackageError{
			ImportStack: stk.Copy(),
			Err:         err,
		}
		embedErr := err.(*EmbedError)
		extTestsErr.setPos(p.Internal.Build.XTestEmbedPatternPos[embedErr.Pattern])
	}
	extTestsIncomplete = extTestsIncomplete || extTestsErr != nil
	stk.Pop()

	// Test package.
	if len(p.TestGoFiles) > 0 || p.Name == "main" || cover != nil && cover.Local {
		withTests = new(Package)
		*withTests = *p
		if withTests.Error == nil {
			withTests.Error = withTestsErr
		}
		withTests.Incomplete = withTests.Incomplete || incomplete
		withTests.ForTest = p.ImportPath
		withTests.GoFiles = nil
		withTests.GoFiles = append(withTests.GoFiles, p.GoFiles...)
		withTests.GoFiles = append(withTests.GoFiles, p.TestGoFiles...)
		withTests.Target = ""
		// Note: The preparation of the vet config requires that common
		// indexes in withTests.Imports and withTests.Internal.RawImports
		// all line up (but RawImports can be shorter than the others).
		// That is, for 0 ≤ i < len(RawImports),
		// RawImports[i] is the import string in the program text, and
		// Imports[i] is the expanded import string (vendoring applied or relative path expanded away).
		// Any implicitly added imports appear in Imports and Internal.Imports
		// but not RawImports (because they were not in the source code).
		// We insert TestImports, imports, and rawTestImports at the start of
		// these lists to preserve the alignment.
		// Note that p.Internal.Imports may not be aligned with p.Imports/p.Internal.RawImports,
		// but we insert at the beginning there too just for consistency.
		withTests.Imports = str.StringList(p.TestImports, p.Imports)
		withTests.Internal.Imports = append(imports, p.Internal.Imports...)
		withTests.Internal.RawImports = str.StringList(rawTestImports, p.Internal.RawImports)
		withTests.Internal.CompiledImports = slices.Clone(p.Internal.CompiledImports)
		for _, path := range withTestsCompiledImports {
			if !slices.Contains(withTests.Internal.CompiledImports, path) {
				withTests.Internal.CompiledImports = append(withTests.Internal.CompiledImports, path)
			}
		}
		withTests.Internal.ForceLibrary = true
		withTests.Internal.BuildInfo = nil
		withTests.Internal.Build = new(build.Package)
		*withTests.Internal.Build = *p.Internal.Build
		m := map[string][]token.Position{}
		for k, v := range p.Internal.Build.ImportPos {
			m[k] = append(m[k], v...)
		}
		for k, v := range p.Internal.Build.TestImportPos {
			m[k] = append(m[k], v...)
		}
		withTests.Internal.Build.ImportPos = m
		if testEmbed == nil && len(p.Internal.Embed) > 0 {
			testEmbed = map[string][]string{}
		}
		maps.Copy(testEmbed, p.Internal.Embed)
		withTests.Internal.Embed = testEmbed
		withTests.EmbedFiles = str.StringList(p.EmbedFiles, p.TestEmbedFiles)
		withTests.Internal.OrigImportPath = p.Internal.OrigImportPath
		withTests.Internal.PGOProfile = p.Internal.PGOProfile
		withTests.Internal.Build.Directives = append(slices.Clip(p.Internal.Build.Directives), p.Internal.Build.TestDirectives...)
	} else {
		withTests = p
	}

	// External test package.
	if len(p.XTestGoFiles) > 0 {
		extTests = &Package{
			PackagePublic: PackagePublic{
				Name:       p.Name + "_test",
				ImportPath: p.ImportPath + "_test",
				Root:       p.Root,
				Dir:        p.Dir,
				Goroot:     p.Goroot,
				GoFiles:    p.XTestGoFiles,
				Imports:    p.XTestImports,
				ForTest:    p.ImportPath,
				Module:     p.Module,
				Error:      extTestsErr,
				Incomplete: extTestsIncomplete,
				EmbedFiles: p.XTestEmbedFiles,
			},
			Internal: PackageInternal{
				LocalPrefix: p.Internal.LocalPrefix,
				Build: &build.Package{
					ImportPos:  p.Internal.Build.XTestImportPos,
					Directives: p.Internal.Build.XTestDirectives,
				},
				Imports:         ximports,
				RawImports:      rawXTestImports,
				CompiledImports: extTestsCompiledImports,

				Asmflags:       p.Internal.Asmflags,
				Gcflags:        p.Internal.Gcflags,
				Ldflags:        p.Internal.Ldflags,
				Gccgoflags:     p.Internal.Gccgoflags,
				Embed:          xtestEmbed,
				OrigImportPath: p.Internal.OrigImportPath,
				PGOProfile:     p.Internal.PGOProfile,
			},
		}
		if extTestsNeedsWithTests {
			extTests.Internal.Imports = append(extTests.Internal.Imports, withTests)
		}
	}

	if variantsOnly {
		return nil, withTests, extTests
	}

	// Arrange for testing.Testing to report true.
	ldflags := append(p.Internal.Ldflags, "-X", "testing.testBinary=1")
	gccgoflags := append(p.Internal.Gccgoflags, "-Wl,--defsym,testing.gccgoTestBinary=1")

	// Build main package.
	testMain = &Package{
		PackagePublic: PackagePublic{
			Name:       "main",
			Dir:        p.Dir,
			GoFiles:    []string{"_testmain.go"},
			ImportPath: p.ImportPath + ".test",
			Root:       p.Root,
			Imports:    str.StringList(TestMainDeps),
			Module:     p.Module,
		},
		Internal: PackageInternal{
			Build:          &build.Package{Name: "main"},
			BuildInfo:      p.Internal.BuildInfo,
			Asmflags:       p.Internal.Asmflags,
			Gcflags:        p.Internal.Gcflags,
			Ldflags:        ldflags,
			Gccgoflags:     gccgoflags,
			OrigImportPath: p.Internal.OrigImportPath,
			PGOProfile:     p.Internal.PGOProfile,
		},
	}

	pb := p.Internal.Build
	testMain.DefaultGODEBUG = defaultGODEBUG(ld, testMain, pb.Directives, pb.TestDirectives, pb.XTestDirectives)

	// The generated main also imports testing, regexp, and os.
	// Also the linker introduces implicit dependencies reported by LinkerDeps.
	stk.Push(ImportInfo{Pkg: "testmain"})
	deps := TestMainDeps // cap==len, so safe for append
	if cover != nil {
		deps = append(deps, "internal/coverage/cfile")
	}
	ldDeps, err := LinkerDeps(ld, p)
	if err != nil && testMain.Error == nil {
		testMain.Error = &PackageError{Err: err}
	}
	for _, d := range ldDeps {
		deps = append(deps, d)
	}
	for _, dep := range deps {
		if dep == withTests.ImportPath {
			testMain.Internal.Imports = append(testMain.Internal.Imports, withTests)
		} else {
			p1, err := loadImport(ld, ctx, opts, pre, dep, "", nil, &stk, nil, 0)
			if err != nil && testMain.Error == nil {
				testMain.Error = err
				testMain.Incomplete = true
			}
			testMain.Internal.Imports = append(testMain.Internal.Imports, p1)
		}
	}
	stk.Pop()

	parallelizablePart := func() {
		// Do initial scan for metadata needed for writing _testmain.go
		// Use that metadata to update the list of imports for package main.
		// The list of imports is used by recompileForTest and by the loop
		// afterward that gathers t.Cover information.
		t, err := loadTestFuncs(p)
		if err != nil && testMain.Error == nil {
			testMain.setLoadPackageDataError(err, p.ImportPath, &stk, nil)
		}
		t.Cover = cover
		if len(withTests.GoFiles)+len(withTests.CgoFiles) > 0 {
			testMain.Internal.Imports = append(testMain.Internal.Imports, withTests)
			testMain.Imports = append(testMain.Imports, withTests.ImportPath)
			t.ImportTest = true
		}
		if extTests != nil {
			testMain.Internal.Imports = append(testMain.Internal.Imports, extTests)
			testMain.Imports = append(testMain.Imports, extTests.ImportPath)
			t.ImportXtest = true
		}

		// Sort and dedup testMain.Imports.
		// Only matters for go list -test output.
		sort.Strings(testMain.Imports)
		w := 0
		for _, path := range testMain.Imports {
			if w == 0 || path != testMain.Imports[w-1] {
				testMain.Imports[w] = path
				w++
			}
		}
		testMain.Imports = testMain.Imports[:w]
		testMain.Internal.RawImports = str.StringList(testMain.Imports)

		// Replace testMain's transitive dependencies with test copies, as necessary.
		cycleErr := recompileForTest(testMain, p, withTests, extTests)
		if cycleErr != nil {
			withTests.Error = cycleErr
			withTests.Incomplete = true
		}

		if !opts.SuppressBuildInfo {
			// Now that testMain.Internal.Imports includes the test dependencies,
			// regenerate build info for the test binary. We can't reuse p's
			// build info because the test variants of packages can add
			// packages from modules that don't already have transitive
			// imports from p.
			testMain.setBuildInfo(ctx, ld.Fetcher(), opts.AutoVCS)
		}

		if cover != nil {
			// Here withTests needs to inherit the proper coverage mode (since
			// it contains p's Go files), whereas testMain contains only
			// test harness code (don't want to instrument it, and
			// we don't want coverage hooks in the pkg init).
			withTests.Internal.Cover.Mode = p.Internal.Cover.Mode
			testMain.Internal.Cover.Mode = "testmain"

			// Should we apply coverage analysis locally, only for this
			// package and only for this test? Yes, if -cover is on but
			// -coverpkg has not specified a list of packages for global
			// coverage.
			if cover.Local {
				withTests.Internal.Cover.Mode = cover.Mode
			}
		}

		data, err := formatTestmain(t)
		if err != nil && testMain.Error == nil {
			testMain.Error = &PackageError{Err: err}
			testMain.Incomplete = true
		}
		// Set TestmainGo even if it is empty: the presence of a TestmainGo
		// indicates that this package is, in fact, a test main.
		testMain.Internal.TestmainGo = &data
	}

	if done != nil {
		go func() {
			parallelizablePart()
			done()
		}()
	} else {
		parallelizablePart()
	}

	return testMain, withTests, extTests
}

// recompileForTest copies and replaces certain packages in testMain's dependency
// graph. This is necessary for two reasons. First, if withTests is different than
// preal, packages that import the package under test should get withTests instead
// of preal. This is particularly important if extTests depends on functionality
// exposed in test sources in withTests. Second, if there is a main package
// (other than testMain) anywhere, we need to set p.Internal.ForceLibrary and
// clear p.Internal.BuildInfo in the test copy to prevent link conflicts.
// This may happen if both -coverpkg and the command line patterns include
// multiple main packages.
func recompileForTest(testMain, preal, withTests, extTests *Package) *PackageError {
	// The "test copy" of preal is withTests.
	// For each package that depends on preal, make a "test copy"
	// that depends on withTests. And so on, up the dependency tree.
	testCopy := map[*Package]*Package{preal: withTests}
	for _, p := range PackageList([]*Package{testMain}) {
		if p == preal {
			continue
		}
		// Copy on write.
		didSplit := p == testMain || p == extTests || p == withTests
		split := func() {
			if didSplit {
				return
			}
			didSplit = true
			if testCopy[p] != nil {
				panic("recompileForTest loop")
			}
			p1 := new(Package)
			testCopy[p] = p1
			*p1 = *p
			p1.ForTest = preal.ImportPath
			p1.Internal.Imports = make([]*Package, len(p.Internal.Imports))
			copy(p1.Internal.Imports, p.Internal.Imports)
			p1.Imports = make([]string, len(p.Imports))
			copy(p1.Imports, p.Imports)
			p = p1
			p.Target = ""
			p.Internal.BuildInfo = nil
			p.Internal.ForceLibrary = true
			p.Internal.PGOProfile = preal.Internal.PGOProfile
		}

		// Update p.Internal.Imports to use test copies.
		for i, imp := range p.Internal.Imports {
			if p1 := testCopy[imp]; p1 != nil && p1 != imp {
				split()

				// If the test dependencies cause a cycle with testMain, this is
				// where it is introduced.
				// (There are no cycles in the graph until this assignment occurs.)
				p.Internal.Imports[i] = p1
			}
		}

		// Force main packages the test imports to be built as libraries.
		// Normal imports of main packages are forbidden by the package loader,
		// but this can still happen if -coverpkg patterns include main packages:
		// covered packages are imported by testMain. Linking multiple packages
		// compiled with '-p main' causes duplicate symbol errors.
		// See golang.org/issue/30907, golang.org/issue/34114.
		if p.Name == "main" && p != testMain && p != withTests {
			split()
		}
		// Split and attach PGO information to test dependencies if preal
		// is built with PGO.
		if preal.Internal.PGOProfile != "" && p.Internal.PGOProfile == "" {
			split()
		}
	}

	return testImportCycle(withTests, withTests)
}

// testImportCycle reports the shortest path by which withTests reaches
// target, as an import cycle error. target is withTests itself once the graph
// holds test copies, and the package without its test files otherwise.
func testImportCycle(withTests, target *Package) *PackageError {
	// Do search to find cycle.
	// importerOf maps each import path to its importer nearest to p.
	importerOf := map[*Package]*Package{}
	for _, p := range withTests.Internal.Imports {
		importerOf[p] = nil
	}

	// q is a breadth-first queue of packages to search for target.
	// Every package added to q has a corresponding entry in pathTo.
	//
	// We search breadth-first for two reasons:
	//
	// 	1. We want to report the shortest cycle.
	//
	// 	2. If p contains multiple cycles, the first cycle we encounter might not
	// 	   contain target. To ensure termination, we have to break all cycles
	// 	   other than the first.
	q := slices.Clip(withTests.Internal.Imports)
	for len(q) > 0 {
		p := q[0]
		q = q[1:]
		if p == target {
			// The stack is supposed to be in the order x imports y imports z.
			// We collect in the reverse order: z is imported by y is imported
			// by x, and then we reverse it.
			var stk ImportStack
			for p != nil {
				importer, ok := importerOf[p]
				if importer == nil && ok { // we set importerOf[p] == nil for the initial set of packages p that are imports of withTests
					importer = withTests
				}
				stk = append(stk, ImportInfo{
					Pkg: p.ImportPath,
					Pos: extractFirstImport(importer.Internal.Build.ImportPos[p.ImportPath]),
				})
				p = importerOf[p]
			}
			// complete the cycle: we set importer[p] = nil to break the cycle
			// in importerOf, it's an implicit importerOf[p] == pTest. Add it
			// back here since we reached nil in the loop above to demonstrate
			// the cycle as (for example) package p imports package q imports package r
			// imports package p.
			stk = append(stk, ImportInfo{
				Pkg: withTests.ImportPath,
			})
			slices.Reverse(stk)
			return &PackageError{
				ImportStack:   stk,
				Err:           errors.New("import cycle not allowed in test"),
				IsImportCycle: true,
			}
		}
		for _, dep := range p.Internal.Imports {
			if _, ok := importerOf[dep]; !ok {
				importerOf[dep] = p
				q = append(q, dep)
			}
		}
	}

	return nil
}

// isTestFunc tells whether fn has the type of a testing function. arg
// specifies the parameter type we look for: B, F, M or T.
func isTestFunc(fn *ast.FuncDecl, arg string) bool {
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 ||
		fn.Type.Params.List == nil ||
		len(fn.Type.Params.List) != 1 ||
		len(fn.Type.Params.List[0].Names) > 1 {
		return false
	}
	ptr, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	// We can't easily check that the type is *testing.M
	// because we don't know how testing has been imported,
	// but at least check that it's *M or *something.M.
	// Same applies for B, F and T.
	if name, ok := ptr.X.(*ast.Ident); ok && name.Name == arg {
		return true
	}
	if sel, ok := ptr.X.(*ast.SelectorExpr); ok && sel.Sel.Name == arg {
		return true
	}
	return false
}

// isTest tells whether name looks like a test (or benchmark, according to prefix).
// It is a Test (say) if there is a character after Test that is not a lower-case letter.
// We don't want TesticularCancer.
func isTest(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if len(name) == len(prefix) { // "Test" is ok
		return true
	}
	rune, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(rune)
}

// loadTestFuncs returns the testFuncs describing the tests that will be run.
// The returned testFuncs is always non-nil, even if an error occurred while
// processing test files.
func loadTestFuncs(withTests *Package) (*testFuncs, error) {
	t := &testFuncs{
		Package: withTests,
	}
	var err error
	for _, file := range withTests.TestGoFiles {
		if lerr := t.load(filepath.Join(withTests.Dir, file), "_test", &t.ImportTest, &t.NeedTest); lerr != nil && err == nil {
			err = lerr
		}
	}
	for _, file := range withTests.XTestGoFiles {
		if lerr := t.load(filepath.Join(withTests.Dir, file), "_xtest", &t.ImportXtest, &t.NeedXtest); lerr != nil && err == nil {
			err = lerr
		}
	}
	return t, err
}

// testMainData is what the generated file is rendered from. One package's tests
// and a whole group's render through the same template: a group is several
// units instead of one.
type testMainData struct {
	Units                 []testUnit
	Cover                 *TestCover
	Covered               string
	CoverSelectedPackages string
}

// formatTestmain returns the content of the _testmain.go file for t.
func formatTestmain(t *testFuncs) ([]byte, error) {
	return renderTestmain(testMainData{
		Units:                 t.Units(),
		Cover:                 t.Cover,
		Covered:               t.Covered(),
		CoverSelectedPackages: t.CoverSelectedPackages(),
	})
}

// renderTestmain writes the generated file for however many units it is given.
func renderTestmain(data testMainData) ([]byte, error) {
	var buf bytes.Buffer
	if err := testmainTmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type testFuncs struct {
	Tests       []testFunc
	Benchmarks  []testFunc
	FuzzTargets []testFunc
	Examples    []testFunc
	TestMain    *testFunc
	Package     *Package
	ImportTest  bool
	NeedTest    bool
	ImportXtest bool
	NeedXtest   bool
	Cover       *TestCover
}

// testUnit is one package's place in a generated test main: the package it
// imports, and the name it imports it under.
//
// A test main names its tests through that alias already, so the func lists it
// writes are package-qualified. The import block is the part that holds one
// package, and this is what lets it hold more.
type testUnit struct {
	// UnitID identifies the package, and ImportPath is what testdeps reports.
	// They differ: ImportPath is EMPTY for command-line-arguments and for a
	// package outside a module, which is what upstream wants testdeps to see,
	// and it cannot also name a unit. -test.unit carries the real path, so
	// UnitID holds that and pickUnit matches on it.
	//
	// TestPath and XTestPath are what the generated file writes in its import
	// block, which is not the same thing once a binary holds several units: two
	// units' test variants cannot both occupy their own path, so each imports a
	// synthetic one that cmd/go maps onto the real variant through importmap.
	UnitID      string
	ImportPath  string
	ModulePath  string
	TestPath    string
	XTestPath   string
	Alias       string
	XAlias      string
	ImportTest  bool
	NeedTest    bool
	ImportXtest bool
	NeedXtest   bool

	Tests       []testFunc
	Benchmarks  []testFunc
	FuzzTargets []testFunc
	Examples    []testFunc
	TestMain    *testFunc

	// Covered names the packages this unit reports coverage for, and
	// CoverSelected is the Go expression listing them. Both belong to the unit
	// rather than the binary: coverage answers for the package under test.
	Covered       string
	CoverSelected string
}

// Units answers the packages this test main imports.
func (funcs *testFuncs) Units() []testUnit {
	return []testUnit{{
		UnitID:        funcs.Package.ImportPath,
		ImportPath:    funcs.ImportPath(),
		ModulePath:    funcs.ModulePath(),
		TestPath:      funcs.Package.ImportPath,
		XTestPath:     funcs.Package.ImportPath + "_test",
		Alias:         testAlias(0, false),
		XAlias:        testAlias(0, true),
		ImportTest:    funcs.ImportTest,
		NeedTest:      funcs.NeedTest,
		ImportXtest:   funcs.ImportXtest,
		NeedXtest:     funcs.NeedXtest,
		Tests:         funcs.Tests,
		Benchmarks:    funcs.Benchmarks,
		FuzzTargets:   funcs.FuzzTargets,
		Examples:      funcs.Examples,
		TestMain:      funcs.TestMain,
		Covered:       funcs.Covered(),
		CoverSelected: funcs.CoverSelectedPackages(),
	}}
}

// testAlias names the import of one package inside a test main. Two packages
// in one main cannot share a name, so the name carries the package's place.
// The first keeps the name a single-package main has always used, which is
// what the go command's own test output and its tests read.
func testAlias(idx int, external bool) string {
	name := "_test"
	if external {
		name = "_xtest"
	}
	if idx == 0 {
		return name
	}
	return fmt.Sprintf("%s%d", name, idx)
}

// ImportPath returns the import path of the package being tested, if it is within GOPATH.
// This is printed by the testing package when running benchmarks.
func (t *testFuncs) ImportPath() string {
	pkg := t.Package.ImportPath
	if strings.HasPrefix(pkg, "_/") {
		return ""
	}
	if pkg == "command-line-arguments" {
		return ""
	}
	return pkg
}

func (t *testFuncs) ModulePath() string {
	m := t.Package.Module
	if m == nil {
		return ""
	}
	return m.Path
}

// Covered returns a string describing which packages are being tested for coverage.
// If the covered package is the same as the tested package, it returns the empty string.
// Otherwise it is a comma-separated human-readable list of packages beginning with
// " in", ready for use in the coverage message.
func (t *testFuncs) Covered() string {
	if t.Cover == nil || t.Cover.Paths == nil {
		return ""
	}
	return " in " + strings.Join(t.Cover.Paths, ", ")
}

func (t *testFuncs) CoverSelectedPackages() string {
	if t.Cover == nil || t.Cover.Paths == nil {
		return `[]string{"` + t.Package.ImportPath + `"}`
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[]string{")
	for k, p := range t.Cover.Pkgs {
		if k != 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, `"%s"`, p.ImportPath)
	}
	sb.WriteString("}")
	return sb.String()
}

// Tested returns the name of the package being tested.
func (t *testFuncs) Tested() string {
	return t.Package.Name
}

type testFunc struct {
	Package   string // imported package name (_test or _xtest)
	Name      string // function name
	Output    string // output, for examples
	Unordered bool   // output is allowed to be unordered.
}

var testFileSet = token.NewFileSet()

func (t *testFuncs) load(filename, pkg string, doImport, seen *bool) error {
	// Pass in the overlaid source if we have an overlay for this file.
	src, err := fsys.Open(filename)
	if err != nil {
		return err
	}
	defer src.Close()
	f, err := parser.ParseFile(testFileSet, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	for _, d := range f.Decls {
		n, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if n.Recv != nil {
			continue
		}
		name := n.Name.String()
		switch {
		case name == "TestMain":
			if isTestFunc(n, "T") {
				t.Tests = append(t.Tests, testFunc{pkg, name, "", false})
				*doImport, *seen = true, true
				continue
			}
			err := checkTestFunc(n, "M")
			if err != nil {
				return err
			}
			if t.TestMain != nil {
				return errors.New("multiple definitions of TestMain")
			}
			t.TestMain = &testFunc{pkg, name, "", false}
			*doImport, *seen = true, true
		case isTest(name, "Test"):
			err := checkTestFunc(n, "T")
			if err != nil {
				return err
			}
			t.Tests = append(t.Tests, testFunc{pkg, name, "", false})
			*doImport, *seen = true, true
		case isTest(name, "Benchmark"):
			err := checkTestFunc(n, "B")
			if err != nil {
				return err
			}
			t.Benchmarks = append(t.Benchmarks, testFunc{pkg, name, "", false})
			*doImport, *seen = true, true
		case isTest(name, "Fuzz"):
			err := checkTestFunc(n, "F")
			if err != nil {
				return err
			}
			t.FuzzTargets = append(t.FuzzTargets, testFunc{pkg, name, "", false})
			*doImport, *seen = true, true
		}
	}
	ex := doc.Examples(f)
	sort.Slice(ex, func(i, j int) bool { return ex[i].Order < ex[j].Order })
	for _, e := range ex {
		*doImport = true // import test file whether executed or not
		if e.Output == "" && !e.EmptyOutput {
			// Don't run examples with no output.
			continue
		}
		t.Examples = append(t.Examples, testFunc{pkg, "Example" + e.Name, e.Output, e.Unordered})
		*seen = true
	}
	return nil
}

func checkTestFunc(fn *ast.FuncDecl, arg string) error {
	var why string
	if !isTestFunc(fn, arg) {
		why = fmt.Sprintf("must be: func %s(%s *testing.%s)", fn.Name.String(), strings.ToLower(arg), arg)
	}
	if fn.Type.TypeParams.NumFields() > 0 {
		why = "test functions cannot have type parameters"
	}
	if why != "" {
		pos := testFileSet.Position(fn.Pos())
		return fmt.Errorf("%s: wrong signature for %s, %s", pos, fn.Name.String(), why)
	}
	return nil
}

var testmainTmpl = lazytemplate.New("main", `
// Code generated by 'go test'. DO NOT EDIT.

package main

import (
{{if gt (len .Units) 1}}
	"fmt"
{{end}}
	"os"
	"reflect"
	"testing"
	"testing/internal/testdeps"
{{if .Cover}}
	"internal/coverage/cfile"
{{end}}

{{range .Units}}
{{if .ImportTest}}
	{{if .NeedTest}}{{.Alias}}{{else}}_{{end}} {{.TestPath | printf "%q"}}
{{end}}
{{if .ImportXtest}}
	{{if .NeedXtest}}{{.XAlias}}{{else}}_{{end}} {{.XTestPath | printf "%q"}}
{{end}}
{{end}}
)

// testUnit is one package's tests. A binary can hold several, and the
// -test.unit flag names the one this process runs.
type testUnit struct {
	unitID      string
	importPath  string
	modulePath  string
	tests       []testing.InternalTest
	benchmarks  []testing.InternalBenchmark
	fuzzTargets []testing.InternalFuzzTarget
	examples    []testing.InternalExample
	testMain    func(*testing.Runner)
{{if .Cover}}
	covered       string
	coverSelected []string
{{end}}
}

var units = []testUnit{
{{range .Units}}
	{
		unitID:     {{.UnitID | printf "%q"}},
		importPath: {{.ImportPath | printf "%q"}},
		modulePath: {{.ModulePath | printf "%q"}},
		tests: []testing.InternalTest{
{{range .Tests}}
			{"{{.Name}}", {{.Package}}.{{.Name}}},
{{end}}
		},
		benchmarks: []testing.InternalBenchmark{
{{range .Benchmarks}}
			{"{{.Name}}", {{.Package}}.{{.Name}}},
{{end}}
		},
		fuzzTargets: []testing.InternalFuzzTarget{
{{range .FuzzTargets}}
			{"{{.Name}}", {{.Package}}.{{.Name}}},
{{end}}
		},
		examples: []testing.InternalExample{
{{range .Examples}}
			{"{{.Name}}", {{.Package}}.{{.Name}}, {{.Output | printf "%q"}}, {{.Unordered}}},
{{end}}
		},
		testMain: {{with .TestMain}}{{.Package}}.{{.Name}}{{else}}nil{{end}},
{{if $.Cover}}
		covered: {{.Covered | printf "%q"}},
		coverSelected: {{printf "%s" .CoverSelected}},
{{end}}
	},
{{end}}
}

{{if gt (len .Units) 1}}
// unitFlag names the package whose tests this process runs. The go command
// passes it when one binary holds more than one package.
const unitFlag = "-test.unit="

// unitEnv names the package when a test started this binary again, which
// names only a test of its own: the process running the package's tests sets
// it, and its children inherit it. When the caller replaced a child's
// environment, package os adds it with unitImplicitEnv, and the child takes
// both out again before its tests run.
const (
	unitEnv         = "GO_TEST_UNIT"
	unitImplicitEnv = "GO_TEST_UNIT_IMPLICIT"
)

// pickUnit answers the package this process runs and takes the flag naming it
// out of the argument list, which the testing package parses next and knows
// nothing about.
func pickUnit() *testUnit {
	want := ""
	kept := make([]string, 0, len(os.Args))
	for _, arg := range os.Args {
		// Spelled out rather than strings.HasPrefix: importing strings here
		// would put it in every test binary's reported import list.
		if len(arg) >= len(unitFlag) && arg[:len(unitFlag)] == unitFlag {
			want = arg[len(unitFlag):]
			continue
		}
		kept = append(kept, arg)
	}
	os.Args = kept

	if want == "" {
		want = os.Getenv(unitEnv)
	}
	if os.Getenv(unitImplicitEnv) == "1" {
		os.Unsetenv(unitEnv)
		os.Unsetenv(unitImplicitEnv)
	}
	if want == "" {
		fmt.Fprintf(os.Stderr, "testing: this binary holds %d packages: name one with %s<import path>\n", len(units), unitFlag)
		os.Exit(2)
	}
	for idx := range units {
		if units[idx].unitID == want {
			testdeps.StartUnit(want)
			return &units[idx]
		}
	}
	fmt.Fprintf(os.Stderr, "testing: this binary holds no tests for %q\n", want)
	os.Exit(2)
	return nil
}
{{else}}
// pickUnit answers the only package in this binary. A binary holding one unit
// takes no -test.unit flag, so it needs no parsing and no fmt: go list reports
// what the generated main imports, and an import added here shows up in every
// test binary the toolchain builds.
func pickUnit() *testUnit { return &units[0] }
{{end}}

func init() {
{{if .Cover}}
	testdeps.CoverMode = {{printf "%q" .Cover.Mode}}
	testdeps.CoverSnapshotFunc = cfile.Snapshot
	testdeps.CoverProcessTestDirFunc = cfile.ProcessCoverTestDir
	testdeps.CoverMarkProfileEmittedFunc = cfile.MarkProfileEmitted

{{end}}
}

func main() {
	unit := pickUnit()
	testdeps.ModulePath = unit.modulePath
	testdeps.ImportPath = unit.importPath
{{if .Cover}}
	testdeps.Covered = unit.covered
	testdeps.CoverSelectedPackages = unit.coverSelected
{{end}}
	runner := testing.MainStart(testdeps.TestDeps{}, unit.tests, unit.benchmarks, unit.fuzzTargets, unit.examples)
	if unit.testMain != nil {
		unit.testMain(runner)
		os.Exit(int(reflect.ValueOf(runner).Elem().FieldByName("exitCode").Int()))
	}
	os.Exit(runner.Run())
}

`)
