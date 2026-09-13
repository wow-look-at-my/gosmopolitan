// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
)

// fileRoots names the packages whose calls resolve a relative path against the
// working directory.
var fileRoots = map[string]bool{
	"os":            true,
	"io/ioutil":     true,
	"path/filepath": true,
	"os/exec":       true,
}

// travelsAlone reports whether a member's tests read the working directory
// while the package initializes.
//
// A shared binary starts once per member, in that member's own directory, and
// every member's initializers run at every one of those starts. So a package
// that opens a file of its own from an initializer fails on every start but
// its own, and the panic ends the process before any test body runs. Such a
// package still runs and still reports; it just costs a whole binary.
func travelsAlone(member TestGroupMember) bool {
	for _, side := range []*Package{member.WithTests, member.ExtTests} {
		if side == nil {
			continue
		}
		if importsAFileRoot(side) && initializesWithACall(side) {
			return true
		}
	}
	return false
}

// importsAFileRoot reports whether a package's test files NAME a package whose
// calls read the working directory. A transitive import does not count: nearly
// every package reaches os that way, and an initializer has to make the call.
func importsAFileRoot(pkg *Package) bool {
	paths := append(append([]string{}, pkg.TestImports...), pkg.XTestImports...)
	for _, path := range append(paths, pkg.Imports...) {
		if fileRoots[path] {
			return true
		}
	}
	return false
}

// initializesWithACall reports whether a package's test files run code as the
// package initializes. A var holding a literal starts the same anywhere, so it
// is not this.
func initializesWithACall(pkg *Package) bool {
	set := token.NewFileSet()
	names := append(append([]string{}, pkg.TestGoFiles...), pkg.XTestGoFiles...)
	for _, name := range names {
		file, err := parser.ParseFile(set, filepath.Join(pkg.Dir, name), nil, 0)
		if err != nil {
			// Unknown initializers, and the group is what is at risk.
			return true
		}
		if declaresAnInitializer(file) {
			return true
		}
	}
	return false
}

// declaresAnInitializer reports whether one file initializes by running code.
func declaresAnInitializer(file *ast.File) bool {
	for _, decl := range file.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc {
			if fn.Recv == nil && fn.Name != nil && fn.Name.Name == "init" {
				return true
			}
			continue
		}
		gen, isGen := decl.(*ast.GenDecl)
		if !isGen || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue {
				continue
			}
			for _, expr := range value.Values {
				if holdsACall(expr) {
					return true
				}
			}
		}
	}
	return false
}

// holdsACall reports whether an expression runs anything to produce its value.
func holdsACall(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if _, isCall := node.(*ast.CallExpr); isCall {
			found = true
		}
		return !found
	})
	return found
}
