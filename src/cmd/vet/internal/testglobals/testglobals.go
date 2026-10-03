// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testglobals defines an Analyzer that reports a test writing to a
// package-level variable without first taking the barrier.
//
// Top level tests run in parallel in this toolchain, so a package variable one
// test writes is a variable every other test in the process reads. The failures
// that produces do not look like races. They look like a test reading a value
// it did not write: context's TestCustomContextGoroutines counted a goroutine
// another test had started, and http2's TestTransportUnknown1xx collected a
// 1xx response belonging to a test running beside it. Both passed on one host
// and failed on another, which is what a shared variable does.
//
// t.Serial stops the other tests for the duration. t.Fork runs the caller alone
// in a child process. Either makes the write private again, so this reports
// only a test that does neither.
package testglobals

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

const Doc = `report a test that writes a package-level variable without t.Serial

Top level tests run in parallel, so a package variable a test writes is
visible to every other test in the process. Call t.Serial to stop the others
for the duration, or t.Fork to run alone in a child process.`

var Analyzer = &analysis.Analyzer{
	Name: "testglobals",
	Doc:  Doc,
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !isTestFunc(pass, fn) {
				continue
			}
			if takesTheBarrier(fn.Body) {
				continue
			}
			for _, w := range packageWrites(pass, fn.Body) {
				pass.Reportf(w.pos, "%s writes package-level variable %s without t.Serial; "+
					"top level tests run in parallel, so another test sees this write",
					fn.Name.Name, w.name)
			}
		}
	}
	return nil, nil
}

// isTestFunc reports whether fn is a test the framework runs: TestXxx taking a
// single *testing.T. A benchmark or a helper is not one -- a helper cannot call
// t.Serial for its caller, and reporting it would name the wrong function.
func isTestFunc(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	if fn.Recv != nil || !isTestName(fn.Name.Name) {
		return false
	}
	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) != 1 {
		return false
	}
	ptr, ok := pass.TypesInfo.TypeOf(params[0].Type).(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Name() == "T" && obj.Pkg() != nil && obj.Pkg().Path() == "testing"
}

// isTestName reports whether name is TestXxx rather than Test or TestXxx's
// lowercase cousins, matching what the testing package itself runs.
func isTestName(name string) bool {
	if len(name) <= len("Test") || name[:len("Test")] != "Test" {
		return false
	}
	c := name[len("Test")]
	return !('a' <= c && c <= 'z')
}

// takesTheBarrier reports whether the body calls t.Serial or t.Fork anywhere,
// including inside a closure. Where it is called does not matter: both hold
// for the rest of the test.
func takesTheBarrier(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == "Serial" || sel.Sel.Name == "Fork" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

type write struct {
	name string
	pos  token.Pos
}

// packageWrites returns every assignment in body whose target is a variable
// declared at package scope.
func packageWrites(pass *analysis.Pass, body *ast.BlockStmt) []write {
	var out []write
	seen := map[string]bool{}
	var note func(ast.Expr)
	note = func(e ast.Expr) {
		id, ok := e.(*ast.Ident)
		if !ok {
			// A field or an index reaches the variable underneath it, so keep
			// walking down to whatever identifier roots the expression.
			switch v := e.(type) {
			case *ast.SelectorExpr:
				note(v.X)
			case *ast.IndexExpr:
				note(v.X)
			case *ast.StarExpr:
				note(v.X)
			}
			return
		}
		obj, ok := pass.TypesInfo.Uses[id].(*types.Var)
		if !ok {
			if obj, ok = pass.TypesInfo.Defs[id].(*types.Var); !ok {
				return
			}
		}
		// Package scope is the package's own scope. A local, a parameter and a
		// field all sit somewhere below it.
		if obj.Parent() == nil || obj.Parent() != obj.Pkg().Scope() {
			return
		}
		if seen[obj.Name()] {
			return
		}
		seen[obj.Name()] = true
		out = append(out, write{name: obj.Name(), pos: id.Pos()})
	}

	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			// := declares, so it cannot target a package variable.
			if s.Tok.String() != ":=" {
				for _, lhs := range s.Lhs {
					note(lhs)
				}
			}
		case *ast.IncDecStmt:
			note(s.X)
		}
		return true
	})
	return out
}
