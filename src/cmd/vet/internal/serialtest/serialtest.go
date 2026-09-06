// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package serialtest defines a vet analyzer for tests that mutate
// process-wide state without taking the serial barrier.
//
// Top-level tests run in parallel in this toolchain, so a test that
// writes a package-level variable races every other test that reads it.
// The failure is a wrong value in an unrelated test, on one run out of
// many. A comment saying which global a test owns stops none of that,
// which is why this is a check.
//
// [testing.T.Serial] stops every other test. [testing.T.Fork] gives the
// caller a process of its own. Setenv and Chdir fork implicitly, so
// either one also answers this check.
package serialtest

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const Doc = `report a test that writes a package-level variable without taking the serial barrier

Tests are parallel by default in this toolchain. A top-level test that
writes a package-level variable, or calls a function in its package that
writes one, must call t.Serial(), t.Fork(), t.Setenv or t.Chdir first.`

var Analyzer = &analysis.Analyzer{
	Name: "serialtest",
	Doc:  Doc,
	Run:  run,
}

// barrierMethods are the T methods that give a test the state to
// itself. Setenv and Chdir are here because each starts a child
// process for the caller, which is a stronger guarantee than Serial.
var barrierMethods = map[string]bool{
	"Serial": true,
	"Fork":   true,
	"Setenv": true,
	"Chdir":  true,
}

func run(pass *analysis.Pass) (any, error) {
	// The testing package's own tests reach the fields this check is
	// about, and cannot call the methods it asks for.
	if pass.Pkg.Path() == "testing" || strings.HasPrefix(pass.Pkg.Path(), "testing/") {
		return nil, nil
	}

	decls := map[*types.Func]*ast.FuncDecl{}
	for _, f := range pass.Files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if obj, ok := pass.TypesInfo.Defs[fd.Name].(*types.Func); ok {
				decls[obj] = fd
			}
		}
	}

	writes := reaches(pass, decls, writesGlobal)
	barriers := reaches(pass, decls, takesBarrier)

	for _, f := range pass.Files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !isTestFunc(pass, fd) {
				continue
			}
			obj := pass.TypesInfo.Defs[fd.Name].(*types.Func)
			if !writes[obj] || barriers[obj] {
				continue
			}
			pass.Reportf(fd.Name.Pos(),
				"%s writes a package-level variable but never calls t.Serial: tests are parallel by default, so another test reads what this one writes",
				fd.Name.Name)
		}
	}
	return nil, nil
}

// reaches reports, for every function declared in this package, whether
// the function has the property that direct answers for, or calls one
// that does. It is the least fixed point of direct over the package's
// own call graph, so a helper three calls down still counts.
func reaches(pass *analysis.Pass, decls map[*types.Func]*ast.FuncDecl, direct func(*analysis.Pass, *ast.FuncDecl) bool) map[*types.Func]bool {
	has := map[*types.Func]bool{}
	callees := map[*types.Func][]*types.Func{}
	for obj, fd := range decls {
		has[obj] = direct(pass, fd)
		callees[obj] = calls(pass, decls, fd)
	}
	for changed := true; changed; {
		changed = false
		for obj, cs := range callees {
			if has[obj] {
				continue
			}
			for _, c := range cs {
				if has[c] {
					has[obj] = true
					changed = true
					break
				}
			}
		}
	}
	return has
}

// calls lists the functions of this package that fd names, whether it
// calls them or takes their value. A function handed to t.Cleanup or
// stored in a variable still runs during the test.
func calls(pass *analysis.Pass, decls map[*types.Func]*ast.FuncDecl, fd *ast.FuncDecl) []*types.Func {
	var out []*types.Func
	seen := map[*types.Func]bool{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		f, ok := pass.TypesInfo.Uses[id].(*types.Func)
		if !ok || seen[f] || decls[f] == nil {
			return true
		}
		seen[f] = true
		out = append(out, f)
		return true
	})
	return out
}

// writesGlobal reports whether fd assigns to a package-level variable:
// one of its own package's, or one it names through an import.
func writesGlobal(pass *analysis.Pass, fd *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok == 0 {
				return true
			}
			for _, lhs := range s.Lhs {
				if isGlobal(pass, lhs) {
					found = true
					return false
				}
			}
		case *ast.IncDecStmt:
			if isGlobal(pass, s.X) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// isGlobal reports whether an assignment to e reaches a variable that
// outlives the test. It follows a selector, an index and a pointer
// dereference to the name the write lands on, so a write to one field
// of a package-level struct counts as much as a write to the whole.
func isGlobal(pass *analysis.Pass, e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.SelectorExpr:
			// A qualified name is a variable of another package. Every
			// other selector is a field, so keep walking to its root.
			if id, ok := x.X.(*ast.Ident); ok {
				if _, ok := pass.TypesInfo.Uses[id].(*types.PkgName); ok {
					v, ok := pass.TypesInfo.Uses[x.Sel].(*types.Var)
					return ok && v.Parent() != nil && v.Parent() == v.Pkg().Scope()
				}
			}
			e = x.X
		case *ast.Ident:
			v, ok := pass.TypesInfo.Uses[x].(*types.Var)
			if !ok {
				return false
			}
			return v.Parent() != nil && v.Pkg() != nil && v.Parent() == v.Pkg().Scope()
		default:
			return false
		}
	}
}

// takesBarrier reports whether fd calls one of the T methods that give
// the caller the process to itself.
func takesBarrier(pass *analysis.Pass, fd *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !barrierMethods[sel.Sel.Name] {
			return true
		}
		if isTestingT(pass.TypesInfo.TypeOf(sel.X)) {
			found = true
			return false
		}
		return true
	})
	return found
}

// isTestFunc reports whether fd is a top-level test: func TestXxx(t *testing.T).
// A benchmark and a fuzz target have no barrier method to call, so
// neither is one.
func isTestFunc(pass *analysis.Pass, fd *ast.FuncDecl) bool {
	if fd.Recv != nil || !strings.HasPrefix(fd.Name.Name, "Test") {
		return false
	}
	if fd.Type.Params == nil || len(fd.Type.Params.List) != 1 {
		return false
	}
	return isTestingT(pass.TypesInfo.TypeOf(fd.Type.Params.List[0].Type))
}

// isTestingT reports whether t is *testing.T.
func isTestingT(t types.Type) bool {
	p, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := p.Elem().(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Name() == "T" && obj.Pkg() != nil && obj.Pkg().Path() == "testing"
}
