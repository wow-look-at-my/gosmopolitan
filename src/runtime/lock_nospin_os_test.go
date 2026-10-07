// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestOSWaitsNeverSpin pins that each port-specific wait below sleeps in the
// OS until its releaser wakes it. None may wait in a loop of yields, short
// sleeps or Gosched calls, which burn the CPU the releaser needs. The files
// are parsed whatever port runs the test, so every port's waits are checked
// on every host.
func TestOSWaitsNeverSpin(t *testing.T) {
	spinCalls := map[string]bool{
		"procyield":    true,
		"osyield":      true,
		"osyield_no_g": true,
		"usleep":       true,
		"usleep_no_g":  true,
		"Gosched":      true,
	}
	sources := []struct {
		path  string
		funcs []string
	}{
		{"os_cosmo.go", []string{
			"futexsleep", "xnuFutexsleep", "futexwakeup", "xnuFutexwakeup",
			"osPreemptExtEnter", "threadStartWait",
			"syscall_runtime_doAllThreadsSyscall", "runPerThreadSyscall",
		}},
		{"os_linux.go", []string{
			"threadStartWait", "syscall_runtime_doAllThreadsSyscall", "runPerThreadSyscall",
		}},
		{"os_windows.go", []string{"osPreemptExtEnter", "preemptExtWait", "preemptM"}},
		{"os_cosmo_nt_preempt.go", []string{"ntPreemptM", "ntPreemptExtRelease"}},
		{"os_cosmo_nt_fd.go", []string{"ntFilePosLock", "ntFilePosUnlock"}},
		{"os_cosmo_nt_lock.go", []string{"ntEmuFcntlLock", "ntLockWait"}},
		{"os_cosmo_nt_msg.go", []string{"ntSockSendVAll", "ntSCMRecvExact", "ntRecvmsgControl"}},
	}
	fset := token.NewFileSet()
	for _, source := range sources {
		file, err := parser.ParseFile(fset, source.path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		bodies := map[string]*ast.BlockStmt{}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				bodies[fn.Name.Name] = fn.Body
			}
		}
		for _, name := range source.funcs {
			body := bodies[name]
			if body == nil {
				t.Errorf("%s: no func %s to check", source.path, name)
				continue
			}
			ast.Inspect(body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ident, ok := call.Fun.(*ast.Ident); ok && spinCalls[ident.Name] {
					t.Errorf("%s: %s calls %s", fset.Position(call.Pos()), name, ident.Name)
				}
				return true
			})
		}
	}
}
