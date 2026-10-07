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

// TestWaitsNeverSpin pins that the runtime's waits for another thread block
// in the OS until that thread wakes them. Each listed function must not
// call or pass along a spin, yield or poll-sleep primitive. A file listed
// without functions is checked whole.
func TestWaitsNeverSpin(t *testing.T) {
	spinNames := map[string]bool{
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
		{"proc.go", []string{
			"main",
			"waitPanicDefers",
			"panicDefersDone",
			"casfrom_Gscanstatus",
			"casgstatus",
			"casGToPreemptScan",
			"casGFromPreempted",
			"execute",
			"checkRunqsNoP",
			"runnextStealAt",
			"runqgrab",
			"runqsteal",
			"preemptSignalDone",
			"syscall_runtime_BeforeExec",
			"profSignalUnlock",
			"setcpuprofilerate",
			"lockextra",
			"extraMSleep",
			"unlockextra",
			"getExtraM",
			"addExtraM",
		}},
		{"preempt.go", []string{"suspendG", "resumeG"}},
		{"mprof.go", []string{
			"goroutineProfileWithLabelsConcurrent",
			"tryRecordGoroutineProfileWB",
			"tryRecordGoroutineProfile",
		}},
		{"coro.go", []string{"coroswitch_m"}},
		{"waitaddr.go", nil},
		{"signalnote.go", nil},
		{"signalnote_cosmo.go", nil},
		{"signalnote_darwin.go", nil},
		{"signalnote_linux.go", nil},
		{"signalnote_sema.go", nil},
		{"signalnote_wasm.go", nil},
		{"extram_sema.go", nil},
		{"extram_sema_aix.go", nil},
		{"extram_sema_darwin.go", nil},
		{"extram_sema_dragonfly.go", nil},
		{"extram_sema_freebsd.go", nil},
		{"extram_sema_futex.go", nil},
		{"extram_sema_netbsd.go", nil},
		{"extram_sema_openbsd.go", nil},
		{"extram_sema_plan9.go", nil},
		{"extram_sema_solaris.go", nil},
		{"extram_sema_wasm.go", nil},
		{"extram_sema_windows.go", nil},
	}
	fset := token.NewFileSet()
	for _, source := range sources {
		file, err := parser.ParseFile(fset, source.path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		bodies := map[string]*ast.FuncDecl{}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				bodies[fn.Name.Name] = fn
			}
		}
		var nodes []ast.Node
		if source.funcs == nil {
			nodes = append(nodes, file)
		}
		for _, name := range source.funcs {
			fn := bodies[name]
			if fn == nil {
				t.Errorf("%s: no func %s to check", source.path, name)
				continue
			}
			nodes = append(nodes, fn)
		}
		for _, node := range nodes {
			ast.Inspect(node, func(node ast.Node) bool {
				if ident, ok := node.(*ast.Ident); ok && spinNames[ident.Name] {
					t.Errorf("%s: uses %s", fset.Position(ident.Pos()), ident.Name)
				}
				return true
			})
		}
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
