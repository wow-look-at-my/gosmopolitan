// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	_ "unsafe"
)

//go:linkname syncRuntimeCanSpin sync.runtime_canSpin
func syncRuntimeCanSpin(iter int) bool

// TestLocksNeverSpin pins that no lock waits by spinning or yielding. A waiter
// that spins burns the CPU its holder needs whenever the holder has been
// descheduled. Every lock sleeps in the OS instead, and its releaser wakes it.
// The check covers each runtime lock implementation, whichever port selects
// it, and the sync.Mutex slow path.
func TestLocksNeverSpin(t *testing.T) {
	spinCalls := map[string]bool{
		"procyield":       true,
		"osyield":         true,
		"runtime_canSpin": true,
		"runtime_doSpin":  true,
	}
	sources := []struct {
		path  string
		funcs []string
	}{
		{"lock_spinbit.go", []string{"lock2", "unlock2", "unlock2Wake"}},
		{"lock_futex.go", []string{"notesleep", "notetsleep_internal", "semasleep"}},
		{"lock_sema.go", []string{"notewakeup", "notesleep", "notetsleep_internal"}},
		{"lock_jsthreads.go", []string{"lock2", "unlock2", "notesleep"}},
		{"lock_js.go", []string{"lock2", "unlock2"}},
		{"lock_wasip1.go", []string{"lock2", "unlock2"}},
		{"../internal/sync/mutex.go", []string{"lockSlow", "unlockSlow"}},
	}
	fset := token.NewFileSet()
	for _, source := range sources {
		file, err := parser.ParseFile(fset, source.path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		declared := map[string]bool{}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				declared[fn.Name.Name] = true
			}
		}
		for _, name := range source.funcs {
			if !declared[name] {
				t.Errorf("%s: no func %s to check", source.path, name)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && spinCalls[ident.Name] {
				t.Errorf("%s: calls %s", fset.Position(call.Pos()), ident.Name)
			}
			return true
		})
	}
}

// TestSyncCanSpinIsFalse pins the answer the runtime gives packages that
// reach sync.runtime_canSpin by linkname: spinning is never granted.
func TestSyncCanSpinIsFalse(t *testing.T) {
	for iter := range 8 {
		if syncRuntimeCanSpin(iter) {
			t.Errorf("sync.runtime_canSpin(%d) = true", iter)
		}
	}
}
