// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package testing

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// TestForkStaysParallel: Fork gives the test a process, not a serial hold. The
// child allows parallelism like any other run, so subtests that ask for it
// must reach a rendezvous only concurrent code can reach.
func TestForkStaysParallel(t *T) {
	if !canFork() {
		t.Skip("this run cannot fork, so Fork takes the barrier")
	}
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
	if !canFork() {
		t.Skip("this run cannot fork, so Fork takes the barrier")
	}
	t.Fork()

	if got := os.Getenv(forkTargetEnv); got != t.Name() {
		t.Fatalf("in the body, %s = %q, want the test's own name %q: the body did not run in a forked child",
			forkTargetEnv, got, t.Name())
	}
}

// TestForkChildSelectsItsTargetByFlag: a child runs one test because
// forkArgs anchors -test.run to it, NOT because anything reads the fork
// marker to filter the test list.
func TestForkChildSelectsItsTargetByFlag(t *T) {
	args := forkArgs("TestOuter", []string{"-test.v"})

	var run string
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "-test.run="); ok {
			run = v
		}
	}
	if !strings.Contains(run, "TestOuter") {
		t.Errorf("forkArgs gave -test.run=%q, want it to name the target", run)
	}
	if !slices.Contains(args, "-test.v") {
		t.Errorf("forkArgs dropped the run's own flags: %q", args)
	}
}

// TestForkFromASubtest: Fork names the subtest, not its parent, so the child's
// -test.run reaches the subtest that asked for it.
func TestForkFromASubtest(t *T) {
	if !canFork() {
		t.Skip("this run cannot fork, so Fork takes the barrier")
	}
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
// child with each other, so a subtest asking for a process of its own. Must
// get one. Each ends up the target of a child of its own, and the run
// terminates. The marker names one test, and every test it runs under stays in
// place rather than forking its own parent.
func TestForkSubtestsGetTheirOwnChild(t *T) {
	if !canFork() {
		t.Skip("this run cannot fork, so Fork takes the barrier")
	}
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

// TestForkReportsTheChildsFailure is the. A test cannot fail itself to prove
// it. It drives runForked directly and checks that a failing child comes
// back as an error naming the test, with the child's output attached.
func TestForkReportsTheChildsFailure(t *T) {
	if !canFork() {
		t.Skip("this run cannot fork, so Fork takes the barrier")
	}
	if os.Getenv(forkTargetEnv) != "" {
		// Some other Fork's child; one process runs one forked test.
		return
	}

	// A name no test has.
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
// to do it. The test asserts the variable is set AND that nothing was stopped.
func TestSetenvForks(t *T) {
	if !canFork() {
		t.Skip("this run cannot fork, so Setenv takes the barrier")
	}
	t.Parallel() // A parallel test shares the process, which is what makes Setenv fork.
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
	t.Parallel() // A parallel test shares the process, which is what makes Chdir fork.
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
// selection. The -target case is the one that matters. Cmd/internal/testdir
// reads it to decide what to compile for, so a child that loses it tests the
// host and reports. That as the answer.
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
			// The whole reason the run's pattern is read rather than dropped: the child must compile what the run named.
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
		// The forked test already names every element the run did, so there is no tail left to carry.
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
	if !canFork() {
		t.Skip("this run cannot fork, so AllocsPerRun takes the barrier")
	}
	// No barrier here, deliberately: sharing the process IS the condition under test.
	t.Parallel()
	if os.Getenv(forkTargetEnv) == "" {
		AllocsPerRun(1, func() {})
		t.Fatal("AllocsPerRun returned in a process this test shares with others; it must fork first")
	}

	var sink any
	if allocs := AllocsPerRun(100, func() { sink = new(int32) }); allocs != 1 {
		t.Errorf("AllocsPerRun(100, new(int32)) = %v, want 1", allocs)
	}
	_ = sink
}

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

// TestAllocsPerRunRefusesBesideASibling. A fork gives the test a process, not
// the process to itself, so the subtests of a forked test still run at the
// same time. A second fork would land in the same place, so the measurement
// refuses here and names the method that stops them.
func TestAllocsPerRunRefusesBesideASibling(t *T) {
	if !canFork() {
		t.Skip("this run cannot fork, so Fork takes the barrier")
	}
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

func TestForkTestLogGivesTheChildItsOwnFile(t *T) {
	args := []string{"bin", "-test.v", "-test.testlogfile=/work/b001/testlog.txt", "-test.run=^TestX$"}
	file, err := forkTestLog(args, "/work/b001/testlog.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(file))
	if file == "/work/b001/testlog.txt" || !strings.HasPrefix(file, os.TempDir()) {
		t.Fatalf("the child's log is %q, want a file of its own under %q", file, os.TempDir())
	}
	want := []string{"bin", "-test.v", "-test.testlogfile=" + file, "-test.run=^TestX$"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}

	split := []string{"bin", "--test.testlogfile", "/work/b001/testlog.txt"}
	file, err = forkTestLog(split, "/work/b001/testlog.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(file))
	if split[2] != file {
		t.Errorf("the flag's own argument is %q, want %q", split[2], file)
	}
}

func TestForkTestLogWithoutALogLeavesTheArgs(t *T) {
	args := []string{"bin", "-test.v"}
	file, err := forkTestLog(args, "")
	if err != nil || file != "" {
		t.Fatalf("forkTestLog = %q, %v; want no file", file, err)
	}
	if !slices.Equal(args, []string{"bin", "-test.v"}) {
		t.Errorf("args = %q, want them as they were", args)
	}
}

// recordedLog takes test log events as internal/testlog's Interface.
type recordedLog struct{ events []string }

func (log *recordedLog) Getenv(key string) { log.events = append(log.events, "getenv "+key) }
func (log *recordedLog) Stat(file string)  { log.events = append(log.events, "stat "+file) }
func (log *recordedLog) Open(file string)  { log.events = append(log.events, "open "+file) }
func (log *recordedLog) Chdir(dir string)  { log.events = append(log.events, "chdir "+dir) }

func TestTakeForkLogRecordsWhatTheChildRead(t *T) {
	scratch := t.TempDir()
	start := filepath.Join(scratch, "pkg")
	moved := filepath.Join(scratch, "elsewhere")
	file := filepath.Join(scratch, "testlog.txt")
	child := "# test log\n" +
		"getenv HOME\n" +
		"open testdata/in.txt\n" +
		"chdir " + moved + "\n" +
		"stat out.txt\n" +
		"open " + filepath.Join(scratch, "abs.txt") + "\n"
	if err := os.WriteFile(file, []byte(child), 0o666); err != nil {
		t.Fatal(err)
	}
	var log recordedLog
	if err := takeForkLog(file, start, &log); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"getenv HOME",
		"open " + filepath.Join(start, "testdata/in.txt"),
		"stat " + moved,
		"stat " + filepath.Join(moved, "out.txt"),
		"open " + filepath.Join(scratch, "abs.txt"),
	}
	if !slices.Equal(log.events, want) {
		t.Errorf("recorded %q, want %q", log.events, want)
	}
}

func TestTakeForkLogRefusesALogItCannotRead(t *T) {
	scratch := t.TempDir()
	for _, tc := range []struct{ name, content string }{
		{"not a test log", "getenv HOME\n"},
		{"a line with no name", "# test log\nopen\n"},
		{"an unknown operation", "# test log\nunlink /x\n"},
	} {
		file := filepath.Join(scratch, strings.ReplaceAll(tc.name, " ", "-"))
		if err := os.WriteFile(file, []byte(tc.content), 0o666); err != nil {
			t.Fatal(err)
		}
		if err := takeForkLog(file, scratch, &recordedLog{}); err == nil {
			t.Errorf("%s: takeForkLog took %q", tc.name, tc.content)
		}
	}
	if err := takeForkLog(filepath.Join(scratch, "absent"), scratch, &recordedLog{}); err == nil {
		t.Error("takeForkLog took a log the child never wrote")
	}
}

// TestWriteTestLogReplacesTheFileWhole: a child that ran with the same
// -test.testlogfile left a longer log there, and the run's own log replaces
// all of it rather than writing over its start.
func TestWriteTestLogReplacesTheFileWhole(t *T) {
	file := filepath.Join(t.TempDir(), "testlog.txt")
	child := "# test log\nopen /a/much/longer/path/than/the/run/reads\nopen /another\n"
	if err := os.WriteFile(file, []byte(child), 0o666); err != nil {
		t.Fatal(err)
	}
	own := "# test log\ngetenv HOME\n"
	if err := writeTestLog(file, []byte(own)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != own {
		t.Errorf("the test log reads %q, want %q", got, own)
	}
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory holds %d files, want the log alone", len(entries))
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
