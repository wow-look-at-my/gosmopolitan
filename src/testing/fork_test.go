// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"os"
	"strings"
	"sync"
	"time"
)

// TestForkStaysParallel: Fork gives the test a process, not a serial hold. The
// child allows parallelism like any other run, so two subtests that ask for it
// must reach a rendezvous only concurrent code can reach.
func TestForkStaysParallel(t *T) {
	t.Fork()

	if serialExclusive.Load() {
		t.Error("Fork took the serial barrier; it must leave the caller parallel")
	}

	var wg sync.WaitGroup
	wg.Add(2)
	met := make(chan struct{})
	go func() {
		wg.Wait()
		close(met)
	}()

	for _, name := range []string{"one", "two"} {
		t.Run(name, func(t *T) {
			t.Parallel()
			wg.Done()
			select {
			case <-met:
			case <-time.After(10 * time.Second):
				t.Errorf("%s waited alone: the subtests of a forked test ran one at a time", t.Name())
			}
		})
	}
}

// TestSubtestsRunInsideRun: a subtest is not parallel unless it asks. Run
// blocks until the subtest returns, so the parent's later statements and its
// deferred calls come after the subtest rather than underneath it.
func TestSubtestsRunInsideRun(t *T) {
	var order []string
	func() {
		defer func() { order = append(order, "parent defer") }()
		t.Run("sub", func(t *T) {
			order = append(order, "sub")
		})
		order = append(order, "after Run")
	}()

	want := "sub, after Run, parent defer"
	if got := strings.Join(order, ", "); got != want {
		t.Errorf("order is %q; want %q", got, want)
	}
}

// TestForkWithSerialIsSerial: Serial is how a forked test asks for the process
// to itself as well, and it still means that inside the child.
func TestForkWithSerialIsSerial(t *T) {
	t.Serial()
	t.Fork()

	if !serialExclusive.Load() {
		t.Error("Serial before Fork must still hold the barrier in the child")
	}
}

// TestForkRunsTheBodyInAChildProcess: the body runs only where the fork
// marker is set, which is a process Fork started for exactly this test.
func TestForkRunsTheBodyInAChildProcess(t *T) {
	t.Fork()

	if got := os.Getenv(forkTargetEnv); got != t.Name() {
		t.Fatalf("in the body, %s = %q, want the test's own name %q: the body did not run in a forked child",
			forkTargetEnv, got, t.Name())
	}
}

// TestForkFromASubtest: Fork names the subtest, not its parent, so the child's
// -test.run reaches the subtest that asked for it.
func TestForkFromASubtest(t *T) {
	t.Run("child", func(t *T) {
		t.Fork()

		if got := os.Getenv(forkTargetEnv); got != t.Name() {
			t.Fatalf("%s = %q, want %q", forkTargetEnv, got, t.Name())
		}
		if !strings.HasSuffix(t.Name(), "/child") {
			t.Fatalf("the forked test is %q, want the subtest", t.Name())
		}
	})
}

// TestForkSubtestsGetTheirOwnChild: the subtests of a forked test share that
// child with each other, so a subtest asking for a process of its own must get
// one. Each ends up the target of a child of its own, and the run terminates:
// the marker names one test, and every test it runs under stays in place
// rather than forking its own parent.
func TestForkSubtestsGetTheirOwnChild(t *T) {
	t.Fork()

	for _, name := range []string{"one", "two"} {
		t.Run(name, func(t *T) {
			t.Fork()

			if got := os.Getenv(forkTargetEnv); got != t.Name() {
				t.Fatalf("%s = %q, want this subtest's own name %q: it shares its parent's child",
					forkTargetEnv, got, t.Name())
			}
		})
	}
}

// TestAllocsPerRunInAForkedSubtest is the reason the rule above exists.
// AllocsPerRun counts the whole process, so a sibling subtest running beside
// the caller counts into the measurement. The measurement used to refuse; it
// now gets the child it asks for.
func TestAllocsPerRunInAForkedSubtest(t *T) {
	t.Fork()

	for _, name := range []string{"one", "two"} {
		t.Run(name, func(t *T) {
			got := AllocsPerRun(10, func() { forkAllocSink = make([]byte, 64) })
			if got != 1 {
				t.Fatalf("AllocsPerRun = %v, want 1: the measurement did not get the process", got)
			}
		})
	}
}

// forkAllocSink keeps the measured allocation from being optimized away.
var forkAllocSink []byte

// TestForkReportsTheChildsFailure is the negative control on the rule that the
// child's exit status is the verdict. A test cannot fail itself to prove it, so
// it drives runForked directly and checks that a failing child comes back as an
// error naming the test, with the child's output attached.
func TestForkReportsTheChildsFailure(t *T) {
	if os.Getenv(forkTargetEnv) != "" {
		// Some other Fork's child; one process runs one forked test.
		return
	}

	// A name no test has. The child then selects nothing, and a run that tests
	// nothing fails in this fork, so the child exits non-zero -- which is the
	// outcome Fork has to notice.
	fake := &T{common: common{name: "TestForkNoSuchTest"}}
	out, err := fake.runForked()
	if err == nil {
		t.Fatalf("the child exited non-zero, so runForked must report it; got nil\n%s", out)
	}
	if !strings.Contains(err.Error(), fake.Name()) {
		t.Errorf("error = %q, want it to name the test that failed", err)
	}
	if len(out) == 0 {
		t.Error("the child's output must reach the caller, so the failure can be read")
	}
}

// TestSetenvForks: Setenv changes the process, and a child is how the test gets
// one of its own. The barrier would give the same isolation and stop the suite
// to do it, so the test asserts the variable is set AND that nothing was
// stopped.
func TestSetenvForks(t *T) {
	if !canFork() {
		t.Skip("this run cannot fork, so Setenv takes the barrier")
	}
	t.Setenv("GO_TEST_SETENV_FORKS", "yes")

	if serialExclusive.Load() {
		t.Error("Setenv took the serial barrier; it must fork and leave the suite running")
	}
	if got := os.Getenv(forkTargetEnv); got != t.Name() {
		t.Fatalf("%s = %q, want %q: Setenv did not fork", forkTargetEnv, got, t.Name())
	}
	if got := os.Getenv("GO_TEST_SETENV_FORKS"); got != "yes" {
		t.Errorf("the variable is %q in the child, want %q", got, "yes")
	}
}

// TestChdirForks is the same rule for the other process-wide change.
func TestChdirForks(t *T) {
	if !canFork() {
		t.Skip("this run cannot fork, so Chdir takes the barrier")
	}
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	if serialExclusive.Load() {
		t.Error("Chdir took the serial barrier; it must fork and leave the suite running")
	}
	if got := os.Getenv(forkTargetEnv); got != t.Name() {
		t.Fatalf("%s = %q, want %q: Chdir did not fork", forkTargetEnv, got, t.Name())
	}
	switch after, err := os.Getwd(); {
	case err != nil:
		t.Fatal(err)
	case after == before:
		t.Errorf("the working directory is still %q: the child did not change it", after)
	}
}

// TestSetenvInAChildStaysInPlace: the marker check covers Setenv too, so a test
// that already has a process of its own sets the variable there. Without it the
// child would fork a grandchild, and this test would not finish.
func TestSetenvInAChildStaysInPlace(t *T) {
	if !canFork() {
		t.Skip("this run cannot fork")
	}
	t.Fork()

	pid := os.Getpid()
	t.Setenv("GO_TEST_SETENV_IN_CHILD", "yes")

	if got := os.Getpid(); got != pid {
		t.Fatalf("Setenv moved the test to pid %d from pid %d: it forked a second time", got, pid)
	}
}

// TestForkArgs: the child inherits the run's arguments and replaces only the
// selection. The -target case is the one that matters -- cmd/internal/testdir
// reads it to decide what to compile for, so a child that loses it tests the
// host and reports that as the answer.
func TestForkArgs(t *T) {
	for _, tc := range []struct {
		name string
		argv []string
		want []string
	}{
		{
			name: "a custom flag is carried",
			argv: []string{"-test.run=TestFoo", "-target=js/wasm"},
			want: []string{"-target=js/wasm", "-test.run=^TestFoo$", "-test.count=1"},
		},
		{
			name: "a selection given as two words is read, not passed on",
			argv: []string{"-test.run", "TestFoo", "-target=js/wasm"},
			want: []string{"-target=js/wasm", "-test.run=^TestFoo$", "-test.count=1"},
		},
		{
			name: "the run's own count does not survive",
			argv: []string{"-test.count=5", "-test.v=true"},
			want: []string{"-test.v=true", "-test.run=^TestFoo$", "-test.count=1"},
		},
		{
			name: "a double dash names the same flag",
			argv: []string{"--test.run=Whatever", "--target=wasip1/wasm"},
			want: []string{"--target=wasip1/wasm", "-test.run=^TestFoo$", "-test.count=1"},
		},
		{
			name: "a word that is not a flag is carried",
			argv: []string{"positional", "-test.short"},
			want: []string{"positional", "-test.short", "-test.run=^TestFoo$", "-test.count=1"},
		},
		{
			// The whole reason the run's pattern is read rather than dropped:
			// the child must compile what the run named, not every subtest
			// under it. This is the shape that ran the testdir suite dry.
			name: "the run's filter on the subtests below survives",
			argv: []string{"-test.run=TestFoo/wasmexport", "-target=js/wasm"},
			want: []string{"-target=js/wasm", "-test.run=^TestFoo$/wasmexport", "-test.count=1"},
		},
	} {
		t.Run(tc.name, func(t *T) {
			got := forkArgs("TestFoo", tc.argv)
			if len(got) != len(tc.want) {
				t.Fatalf("forkArgs(%q) = %q, want %q", tc.argv, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("forkArgs(%q) = %q, want %q", tc.argv, got, tc.want)
				}
			}
		})
	}
}

func TestForkRunValue(t *T) {
	for _, tc := range []struct{ name, run, want string }{
		{"Test", "", "^Test$"},
		{"Test", "Test", "^Test$"},
		{"Test", "Test/wasmexport", "^Test$/wasmexport"},
		{"Test", "Test/wasmexport/deeper", "^Test$/wasmexport/deeper"},
		// The forked test already names every element the run did, so there is
		// no tail left to carry.
		{"Test/wasmexport", "Test/wasmexport", "^Test$/^wasmexport$"},
	} {
		if got := forkRunValue(tc.name, tc.run); got != tc.want {
			t.Errorf("forkRunValue(%q, %q) = %q, want %q", tc.name, tc.run, got, tc.want)
		}
	}
}

// TestAllocsPerRunForks: AllocsPerRun measures the whole process, so a caller
// that shares it forks. Tests are parallel by default, so this test is such a
// caller: the measurement below runs only in a child that runs this test alone.
func TestAllocsPerRunForks(t *T) {
	if os.Getenv(forkTargetEnv) == "" {
		AllocsPerRun(1, func() {})
		t.Fatal("AllocsPerRun returned in a process this test shares with others; it must fork first")
	}

	if allocs := AllocsPerRun(100, func() { allocsSink = new(int32) }); allocs != 1 {
		t.Errorf("AllocsPerRun(100, new(int32)) = %v, want 1", allocs)
	}
}

var allocsSink any

// TestAllocsPerRunUnderSerialDoesNotFork: a serial test already has the process
// to itself, so the measurement happens right here. A fork would run the rest
// of this test in a child, where the marker is set.
func TestAllocsPerRunUnderSerialDoesNotFork(t *T) {
	t.Serial()

	AllocsPerRun(1, func() {})
	if got := os.Getenv(forkTargetEnv); got != "" {
		t.Fatalf("%s = %q: AllocsPerRun forked a serial test", forkTargetEnv, got)
	}
}

// TestAllocsPerRunRefusesBesideASibling: a fork gives the test a process, not
// the process to itself, so the subtests of a forked test still run at the
// same time. A second fork would land in the same place, so the measurement
// refuses here and names the method that stops them.
func TestAllocsPerRunRefusesBesideASibling(t *T) {
	t.Fork()

	running, release := make(chan struct{}), make(chan struct{})
	t.Run("busy", func(t *T) {
		t.Parallel()
		close(running)
		<-release
	})
	t.Run("measuring", func(t *T) {
		t.Parallel()
		<-running
		defer close(release)
		defer func() {
			err, ok := recover().(error)
			if !ok {
				t.Fatal("AllocsPerRun measured while a sibling subtest was running")
			}
			if !strings.Contains(err.Error(), "t.Serial") {
				t.Errorf("panic says %q; it must name t.Serial, which is the fix", err)
			}
		}()
		AllocsPerRun(1, func() {})
	})
}

func TestForkRunPattern(t *T) {
	for _, tc := range []struct{ name, want string }{
		{"TestFoo", "^TestFoo$"},
		{"TestFoo/sub", "^TestFoo$/^sub$"},
		{"TestFoo/a/b", "^TestFoo$/^a$/^b$"},
		{"TestA+B", `^TestA\+B$`},
		{"TestRe(x)[y]", `^TestRe\(x\)\[y\]$`},
		{"Test.Name", `^Test\.Name$`},
	} {
		if got := forkRunPattern(tc.name); got != tc.want {
			t.Errorf("forkRunPattern(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
