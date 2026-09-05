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
// child runs parallel-by-default like any other run, so the subtests of a
// forked test must reach a rendezvous only concurrent code can reach.
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
			wg.Done()
			select {
			case <-met:
			case <-time.After(10 * time.Second):
				t.Errorf("%s waited alone: the subtests of a forked test ran one at a time", t.Name())
			}
		})
	}
}

// TestForkWithSerialIsSerial: Serial is how a forked test asks for the process
// to itself as well, and it still means that inside the child.
func TestForkWithSerialIsSerial(t *T) {
	t.Serial("this checks that an exclusive barrier hold survives into a child process, so nothing else may hold it")
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

// TestForkDoesNotForkAgain: a child runs its own subtests in place. Without the
// marker check in Fork, each of these would start another process, and this
// test would not finish.
func TestForkDoesNotForkAgain(t *T) {
	t.Fork()

	pid := os.Getpid()
	for _, name := range []string{"one", "two"} {
		t.Run(name, func(t *T) {
			t.Fork()

			if got := os.Getpid(); got != pid {
				t.Fatalf("subtest ran in pid %d, want its parent's pid %d: it forked a second time", got, pid)
			}
		})
	}
}

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
	t.Serial("a measurement taken while the barrier is held must not fork, which only holding it here proves")

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
		close(running)
		<-release
	})
	t.Run("measuring", func(t *T) {
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
