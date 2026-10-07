// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"runtime"
	"testing"
)

// TestSignalAndTraceWaitsNeverSpin pins that the waits in the profiling,
// signal and tracing paths block in the OS and are woken by whoever ends
// them, rather than spinning, yielding or polling with a sleep. Each
// function named below must exist and must not call any of the
// wait-by-retrying primitives.
func TestSignalAndTraceWaitsNeverSpin(t *testing.T) {
	spinCalls := map[string]bool{
		"procyield":    true,
		"osyield":      true,
		"osyield_no_g": true,
		"usleep":       true,
		"usleep_no_g":  true,
		"Gosched":      true,
		"sched_yield":  true,
		"timeSleep":    true,
	}
	sources := []struct {
		path  string
		funcs []string
	}{
		{"cpuprof.go", []string{"add", "addNonGo", "runtime_pprof_readProfile"}},
		{"tracecpu.go", []string{"traceCPUSample"}},
		{"sigqueue.go", []string{"sigsend", "signal_recv", "signalWaitUntilIdle", "sigNotifyReceiver", "sigDeliveryDone"}},
		{"signal_unix.go", []string{"dieFromSignal", "raisebadsignal", "crashWaitForMs"}},
		{"trace.go", []string{"StartTrace", "traceAdvance"}},
		{"traceruntime.go", []string{"traceRelease", "traceWriterDone", "traceExitedSyscall"}},
		{"lock_wasip1.go", []string{"notewakeup", "notetsleepg"}},
		{"lock_jsthreads.go", []string{"wasmWorkerParkNote", "wasmWorkerUnpark", "beforeIdle", "wasmMainParkArmBackstop"}},
		{"netpoll_wasip1.go", []string{"netpoll"}},
	}
	fset := token.NewFileSet()
	for _, source := range sources {
		file, err := parser.ParseFile(fset, source.path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		wanted := map[string]bool{}
		for _, name := range source.funcs {
			wanted[name] = true
		}
		found := map[string]bool{}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !wanted[fn.Name.Name] || fn.Body == nil {
				continue
			}
			found[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ident, ok := call.Fun.(*ast.Ident); ok && spinCalls[ident.Name] {
					t.Errorf("%s: %s calls %s", fset.Position(call.Pos()), fn.Name.Name, ident.Name)
				}
				return true
			})
		}
		for _, name := range source.funcs {
			if !found[name] {
				t.Errorf("%s: no func %s to check", source.path, name)
			}
		}
	}
}

// TestCPUProfileSampleNeverWaitsForLock pins that a profiling signal that
// finds prof.signalLock held drops its sample and counts it, rather than
// waiting for the holder, which may be descheduled or be the very code the
// signal interrupted.
func TestCPUProfileSampleNeverWaitsForLock(t *testing.T) {
	if dropped := runtime.CPUProfileAddContended(); dropped != 2 {
		t.Errorf("add and addNonGo under a held prof.signalLock counted %d dropped samples, want 2", dropped)
	}
}
