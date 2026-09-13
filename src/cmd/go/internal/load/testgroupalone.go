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

// cgiPath is the package whose Handler starts a program as a CGI script.
const cgiPath = "net/http/cgi"

// travelsAlone reports whether a member cannot share a binary. Such a package
// still runs and still reports; it just costs a whole binary.
//
// A shared binary starts once per member, in that member's own directory, and
// every member's initializers run at every one of those starts. So a package
// that opens a file of its own from an initializer fails on every start but
// its own, and the panic ends the process before any test body runs.
//
// A shared binary a test starts again finds its member in the environment it
// inherits. A CGI script inherits none (RFC 3875 builds it from the request),
// so it reaches the member's tests only when the binary holds nothing else.
func travelsAlone(member TestGroupMember) bool {
	for _, side := range []*Package{member.WithTests, member.ExtTests} {
		if side == nil {
			continue
		}
		if importsAFileRoot(side) && initializesWithACall(side) {
			return true
		}
		if servesCGI(side) && namesItsOwnBinary(side) {
			return true
		}
	}
	return false
}

// servesCGI reports whether a package's tests reach the CGI handler: the tests
// are the handler's own, or they import it.
func servesCGI(pkg *Package) bool {
	if pkg.ImportPath == cgiPath {
		return true
	}
	paths := append(append([]string{}, pkg.TestImports...), pkg.XTestImports...)
	for _, path := range append(paths, pkg.Imports...) {
		if path == cgiPath {
			return true
		}
	}
	return false
}

// namesItsOwnBinary reports whether a package's test files name the running
// executable, through os.Args or an Executable call, which is how a test hands
// its own binary to something that starts it.
func namesItsOwnBinary(pkg *Package) bool {
	set := token.NewFileSet()
	names := append(append([]string{}, pkg.TestGoFiles...), pkg.XTestGoFiles...)
	for _, name := range names {
		file, err := parser.ParseFile(set, filepath.Join(pkg.Dir, name), nil, 0)
		if err != nil {
			// Unknown contents, and the group is what is at risk.
			return true
		}
		if selectsOwnBinary(file) {
			return true
		}
	}
	return false
}

// selectsOwnBinary reports whether one file reads os.Args or calls a function
// named Executable.
func selectsOwnBinary(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		sel, isSel := node.(*ast.SelectorExpr)
		if !isSel {
			return !found
		}
		if sel.Sel.Name == "Executable" {
			found = true
		}
		if owner, isIdent := sel.X.(*ast.Ident); isIdent && owner.Name == "os" && sel.Sel.Name == "Args" {
			found = true
		}
		return !found
	})
	return found
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
