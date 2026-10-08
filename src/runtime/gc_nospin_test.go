// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// osYieldCalls give the CPU back for a while and come back without anyone
// waking the caller. A wait built on them spins against a holder the OS
// may have descheduled.
var osYieldCalls = map[string]bool{
	"procyield":    true,
	"osyield":      true,
	"osyield_no_g": true,
	"usleep":       true,
	"usleep_no_g":  true,
}

// schedYieldCalls hand the P to another goroutine and requeue the caller,
// which runs again without anyone waking it.
var schedYieldCalls = map[string]bool{
	"Gosched":       true,
	"goschedIfBusy": true,
	"goyield":       true,
}

// loadCalls only read state. A loop whose calls are all loads changes
// nothing, so it can end only by another thread's write.
var loadCalls = map[string]bool{
	"Load":        true,
	"Load8":       true,
	"Load64":      true,
	"Loadp":       true,
	"LoadAcquire": true,
	"load":        true,
	"isSweepDone": true,
	"isDone":      true,
	"split":       true,
	"head":        true,
	"tail":        true,
	"Goid":        true,
	"uintptr":     true,
	"uint32":      true,
	"uint64":      true,
	"int32":       true,
	"int64":       true,
	"Pointer":     true,
}

// failCalls report a bad state and end the program. A loop does no work
// by calling them.
var failCalls = map[string]bool{
	"throw":   true,
	"Throw":   true,
	"fatal":   true,
	"print":   true,
	"println": true,
}

// claimCalls try to take a state another thread holds.
var claimCalls = map[string]bool{
	"CompareAndSwap": true,
	"Cas":            true,
	"Casp1":          true,
}

func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.IndexExpr:
		if sel, ok := fun.X.(*ast.SelectorExpr); ok {
			return sel.Sel.Name
		}
	}
	return ""
}

// waitLoopReason reports why loop waits for another thread without
// sleeping, or "" if it does not. A loop waits by yielding when it calls
// a scheduler yield and otherwise only loads or claims state. A loop
// busy-waits when every call in it is a load.
func waitLoopReason(loop *ast.ForStmt) string {
	yields, others := 0, 0
	onlyLoads := true
	ast.Inspect(loop, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calleeName(call)
		switch {
		case schedYieldCalls[name]:
			yields++
		case loadCalls[name], failCalls[name]:
		case claimCalls[name]:
			onlyLoads = false
		default:
			onlyLoads = false
			others++
		}
		return true
	})
	if yields > 0 && others == 0 {
		return "waits by yielding the P"
	}
	if yields == 0 && onlyLoads {
		return "busy-waits on loads"
	}
	return ""
}

// TestGCWaitsNeverSpin pins that each GC-side wait sleeps in the OS or
// parks, and is woken by whoever ends it: no wait yields the thread or
// the P and comes back to look again, and no loop spins on loads. A
// function named here is checked for any OS yield call and for any loop
// that waits without sleeping.
func TestGCWaitsNeverSpin(t *testing.T) {
	sources := []struct {
		path  string
		funcs []string
	}{
		{"mgc.go", []string{"GC"}},
		{"mgcsweep.go", []string{"mspan.ensureSwept", "mspan.waitSwept", "mspan.publishSwept", "activeSweep.waitDone", "activeSweep.end"}},
		{"mstats.go", []string{"consistentHeapStats.acquire", "consistentHeapStats.release", "consistentHeapStats.read", "consistentHeapStats.waitWriter", "consistentHeapStats.wakeReader"}},
		{"type.go", []string{"getGCMaskOnDemand"}},
		{"mspanset.go", []string{"spanSet.push", "spanSet.pop"}},
		{"../internal/runtime/exithook/hooks.go", []string{"Add", "Run"}},
	}
	fset := token.NewFileSet()
	for _, source := range sources {
		file, err := parser.ParseFile(fset, source.path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		declared := map[string]*ast.FuncDecl{}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil {
				recv := fn.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if ident, ok := recv.(*ast.Ident); ok {
					name = ident.Name + "." + name
				}
			}
			declared[name] = fn
		}
		for _, name := range source.funcs {
			fn := declared[name]
			if fn == nil {
				t.Errorf("%s: no func %s to check", source.path, name)
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.CallExpr:
					if callee := calleeName(node); osYieldCalls[callee] {
						t.Errorf("%s: %s calls %s", fset.Position(node.Pos()), name, callee)
					}
				case *ast.ForStmt:
					if reason := waitLoopReason(node); reason != "" {
						t.Errorf("%s: loop in %s %s", fset.Position(node.Pos()), name, reason)
					}
				}
				return true
			})
		}
	}
}

// widenProcs makes sure two Ms can run Go code at once, so a test whose
// waiter sleeps holding its P still has a P for the goroutine that wakes
// it. It returns the setting to restore.
func widenProcs() int {
	procs := runtime.GOMAXPROCS(0)
	if procs < 2 {
		runtime.GOMAXPROCS(2)
	}
	return procs
}

var nospinSink []byte

// TestEnsureSweptSleepsUntilPublished pins that ensureSwept on a span
// another sweeper owns sleeps until that sweeper publishes the span, and
// that the publish is what wakes it. On a single-threaded runtime no
// other thread can own the span, so the same claim is a self-deadlock
// that ensureSwept throws on, and the publish clears it.
func TestEnsureSweptSleepsUntilPublished(t *testing.T) {
	t.Serial() // A GC while the span is claimed would find it mid-sweep.
	defer runtime.GOMAXPROCS(widenProcs())
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	runtime.GC()

	nospinSink = make([]byte, 128<<10)
	claim := runtime.ClaimSweptSpan(unsafe.Pointer(&nospinSink[0]))
	if runtime.SingleThreadedRuntime {
		if !claim.WaitDeadlocks() {
			t.Error("a span claimed on the only thread does not read as a self-deadlock")
		}
		claim.Publish()
		if claim.WaitDeadlocks() {
			t.Error("a published span still reads as a self-deadlock")
		}
		claim.EnsureSwept()
		nospinSink = nil
		return
	}
	if claim.WaitDeadlocks() {
		t.Fatal("a span claimed with other threads available reads as a self-deadlock")
	}
	var published atomic.Bool
	go func() {
		for runtime.SpanSweepWaiters() == 0 {
			time.Sleep(time.Millisecond)
		}
		published.Store(true)
		claim.Publish()
	}()
	claim.EnsureSwept()
	if !published.Load() {
		t.Fatal("ensureSwept returned before the sweeper published the span")
	}
	if waiters := runtime.SpanSweepWaiters(); waiters != 0 {
		t.Fatalf("%d Ms left waiting after the publish", waiters)
	}
	nospinSink = nil
}

// TestHeapStatsReadSleepsOnWriter pins that a heap-stats reader facing
// a P inside a write section marks that P and sleeps until the writer's
// release, rather than spinning on its sequence number. On a
// single-threaded runtime a section open while the stats are read is the
// reader's own, so read throws on it rather than waiting.
func TestHeapStatsReadSleepsOnWriter(t *testing.T) {
	t.Serial() // A stop-the-world while the write section is open would wait on it.
	defer runtime.GOMAXPROCS(widenProcs())
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	open, closed := runtime.HeapStatsWriterWaitDeadlocks()
	if closed {
		t.Error("a closed write section reads as a self-deadlock")
	}
	if runtime.SingleThreadedRuntime {
		if !open {
			t.Error("a write section open on the only thread does not read as a self-deadlock")
		}
		return
	}
	if open {
		t.Error("a write section open with other threads available reads as a self-deadlock")
	}
	if !runtime.HeapStatsReadSleepsOnWriter(int64(time.Minute)) {
		t.Fatal("the reader did not sleep on the open write section until its release")
	}
}

type nospinObject struct {
	payload [8]*int
}

// TestConcurrentGCWaitsForSweep runs GC from several goroutines while
// others allocate and attach finalizers and cleanups. GC waits for the
// sweepers it does not own, and attaching a special waits for a span
// another sweeper holds, so every wait has a waker to find.
func TestConcurrentGCWaitsForSweep(t *testing.T) {
	t.Serial() // GOMAXPROCS is process-wide.
	procs := runtime.GOMAXPROCS(0)
	if procs < 4 {
		runtime.GOMAXPROCS(4)
	}
	defer runtime.GOMAXPROCS(procs)

	stop := make(chan struct{})
	var allocators sync.WaitGroup
	for range 4 {
		allocators.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				batch := make([]*nospinObject, 256)
				for idx := range batch {
					object := &nospinObject{}
					switch idx % 8 {
					case 0:
						runtime.SetFinalizer(object, func(*nospinObject) {})
					case 1:
						runtime.AddCleanup(object, func(int) {}, idx)
					}
					batch[idx] = object
				}
				runtime.KeepAlive(batch)
			}
		})
	}
	var collectors sync.WaitGroup
	for range 4 {
		collectors.Go(func() {
			for range 25 {
				runtime.GC()
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
			}
		})
	}
	collectors.Wait()
	close(stop)
	allocators.Wait()
}
