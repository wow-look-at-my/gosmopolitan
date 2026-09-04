// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"os"
	"strings"
)

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
