// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"strconv"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/syntax"
	"cmd/internal/src"
)

// PlainImports holds, under -testinit, the packages that the package's
// non-test files import. Only those are initialized before the package: its
// test files are initialized later, when the tests of the -testinit package
// run.
var PlainImports map[string]bool

// importsTested reports, under -testinit, whether an external test package
// imports the package under test.
var importsTested bool

// recordPlainImports fills PlainImports and importsTested from the parsed
// files.
func recordPlainImports(filenames []string, noders []*noder) {
	if base.Flag.TestInit == "" {
		return
	}
	PlainImports = make(map[string]bool)
	for idx, name := range filenames {
		for _, decl := range noders[idx].file.DeclList {
			imp, isImport := decl.(*syntax.ImportDecl)
			if !isImport || imp.Path == nil {
				continue
			}
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue // the type checker reports it
			}
			resolved, err := resolveImportPath(path)
			if err != nil {
				continue // the type checker reports it
			}
			if resolved == base.Flag.TestInit {
				importsTested = true
			}
			if !isTestFileName(name) {
				PlainImports[resolved] = true
			}
		}
	}
}

// importTestedFirst reads the export data of the package under test before
// any other, when an external test package imports it. That export data
// holds what the package's own _test.go files add, methods included. Every
// other package here was compiled against the package without them, and
// whichever export data names a type first defines it for the whole compile.
func importTestedFirst(importer *gcimports) {
	if !importsTested || base.Ctxt.Pkgpath == base.Flag.TestInit {
		return
	}
	if _, err := importer.ImportFrom(base.Flag.TestInit, "", 0); err != nil {
		base.Fatalf("importing %s: %v", base.Flag.TestInit, err)
	}
}

// DeferredToTests reports whether a declaration at pos is initialized with
// the tests rather than with the package: -testinit is set and the
// declaration is in a _test.go file.
func DeferredToTests(pos src.XPos) bool {
	if base.Flag.TestInit == "" {
		return false
	}
	return isTestFileName(base.Ctxt.PosTable.Pos(pos).Filename())
}

func isTestFileName(name string) bool {
	return strings.HasSuffix(name, "_test.go")
}
