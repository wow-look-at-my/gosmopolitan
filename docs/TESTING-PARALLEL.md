# Tests are parallel by default

`src/testing` in this fork runs every test and subtest as if it had called `t.Parallel()`, which is now a no-op. Two
methods opt a test out of that, and they buy the same isolation at different prices: `t.Serial()` keeps the test in
this process and stops every other test, `t.Fork()` gives the test a process of its own and lets the suite run on.

A test failing only under this fork's `go test` is almost always one of these.

## t.Serial

A test that mutates process-wide state - a package global, the environment, the working directory, `GOMAXPROCS` -
calls `t.Serial(reason)`, which waits for every other test to stop and runs the caller alone until it returns;
`t.Setenv`, `t.Chdir` and `cryptotest.SetGlobalRandom` take it themselves. A serial test's subtests run one at a time
under its hold.

Depth: DEBUGGING.md "tests parallel by default" (2026-09-02).

### The reason argument

```go
t.Serial("cfg.BuildX is a package global, and turning it on changes what every other build prints")
```

The reason is what the next reader has instead of the shared state, which is invisible from the call. `Serial` checks
it and **warns**, then runs the test alone anyway - a suite must not fail over its own prose, and refusing to
serialize a test that asked to be serialized would run it against the state it is guarding. Four rules, all reported
against the calling test as `warning: t.Serial: ...`:

| Rule | Why |
|---|---|
| A reason is given | An unexplained serial test is a `-parallel 1` nobody can review. |
| At least 48 characters | "process-wide state" and "AllocsPerRun" name a category, not this test's problem. |
| Does not repeat the test's name or file | Both sit next to the warning already; repeating them spends the length saying nothing. |
| At most 98% the same as another reason in the binary | This is the rule with teeth. |

The similarity rule is the reason the others are worth having. One pasted sentence is how a package quietly stops
being parallel: each call looks defensible on its own, and the 84th one costs as much as the first. Two tests that
serialize for the same reason usually want `t.Fork` instead, which gives each a process and stops nobody.

Mechanics: reasons are compared normalized - lowercased, with runs of non-alphanumerics collapsed - so case and
punctuation do not make one reason look like two. The score is Levenshtein distance over the length of the longer
reason. The registry is per test binary, so the bound is a statement about one package. A call site registers once,
so a `Serial` in a loop or in a table-driven subtest never reports itself as its own duplicate.

Implementation: `src/testing/serialreason.go`; tests in `src/testing/serialreason_test.go`.

## t.Fork

`t.Fork()` runs the rest of the caller in a CHILD PROCESS, alone, and takes the child's exit status as the test's
verdict. It returns in the child, where the body then runs; in the parent it does not return.

Reach for it over Serial when the state is process-global and SHARED - a package variable, a metrics registry, a
counter another test also writes - because a child starts with its own copy that no other test can reach. Serial
only guarantees that nothing else runs *at the same time*; it does not give the test its own copy of anything, so a
counter another test already advanced is still advanced. Fork also leaves the rest of the suite running, where
Serial stops it.

Fork does NOT serialize. The child runs parallel-by-default like any other run, so a forked test's subtests still run
at the same time, and the rest of the suite keeps running in the parent. A test that needs a process of its own AND
the process to itself calls `t.Serial()` as well - in either order, since the child runs the body from the top.

Mechanics:

- The child re-runs that one test via `-test.run`, anchored per slash-separated name element, so a subtest that
  calls Fork gets a child running exactly that subtest.
- The child carries the marker environment variable `GO_TEST_FORK_TARGET`, naming the test it was started for. Its
  presence is also how a child knows never to fork again, so a forked test that runs subtests - or whose subtests
  call Fork - still runs in exactly one child.
- The child inherits the environment plus `-test.v`, `-test.timeout`, `-test.short`, `-test.fullpath` and
  `-test.gocoverdir`, so verbose output still appears and the child's coverage counts toward the same profile.
- The child's output becomes the test's output, and a child that cannot be started, or that dies on a signal, is
  reported against the test.

`testing.AllocsPerRun` forks itself over this machinery. Its count and its `GOMAXPROCS(1)` are process-wide, so a
caller that shares the process with other tests measures nothing meaningful. It takes no `*T` and cannot call `Fork`,
so it panics with the internal `allocsFork` value; `tRunner` already recovers, and it forks there instead of dying.
The child re-runs that one test alone and measures. A caller that already has the process - a forked child, a serial
test, or a run with no parallel test in flight - measures in place. Where the panic reaches any other recover, or a
goroutine that is no test, its message names `t.Fork` and `t.Serial`.

It is fork+exec, not `fork(2)`: the child is a fresh process re-running the test binary. A bare `fork(2)` is not
safe in Go - the child would inherit only the calling thread, with the runtime's other threads gone and any lock
they held at fork time held forever - and it would be the wrong semantics anyway, since copy-on-write would hand
the child the parent's counters at their current values, which is the contamination Fork exists to escape.

Implementation: `Fork`, `runForked` and `forkRunPattern` in `src/testing/testing.go`; tests in
`src/testing/fork_test.go`.
