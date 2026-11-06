# CI

Per-step rationale trimmed from `cosmo-ci.yml`'s comments (1-line cap): docs/CI.md.

**A test is never skipped.** Not with `-run`. Not with `t.Skip`. Not with a build tag. Not by dropping a port. A package that reports success in a second because its tests never ran is worse than a red one.

**A vendored third-party package's test files do not run, and every suite says so.** The vendor submodules carry tests that `go mod vendor` will strip. They cannot build here. The go command lists them under `IgnoredGoFiles` (`cmd/go/internal/load`). Org modules keep their tests. `dist test` ends with a `##### Test coverage` block: the share of packages whose tests run, and each untested module.

CI puts it first in the suite leg's step summary.

**A slow suite is never made fast by running less of it.** Every port is fast, or it gets made fast. No tier of platforms is allowed to be slow. Fix what makes each test expensive instead. `cmd/internal/testdir` runs its whole corpus in 221.3s, against 1265.8s for the same corpus one program at a time. Its run programs compile into one executable and run in one process.

**A dependency is never vendored.** Its source does not belong in this tree. A vendor directory writes each version down a second time in `modules.txt`. The gitlink writes it a third time. The go command then refuses to build when the three disagree. That duplication is what broke `cmd/go`.

`src/cmd` still builds in vendor mode today. Removing that is task work. It is not a licence to add anything to the vendor tree.

**An org dependency follows its branch head, and a CI run locks that head.** `go.mod` keeps the placeholder `v0.0.0`, and the go command resolves the head in memory. It silently records only the third-party requirements the org module declares. A CI build is `GITHUB_ACTIONS=true` with no coding agent in the environment or among the ancestors. There the first command of a run records each head in the run lock store. Every later command of that run builds the recorded version.

A store that fails fails the build. Which head, and the rest: docs/ORG-DEPS.md. A gitlink never pins an org submodule. The cache client's frozen gitlink is what broke `cmd/go`: `shared.go` named counters that commit did not carry, and no toolchain built. Every `wow-look-at-my` submodule follows a branch. `submodulebranch.bash` names that branch, then runs `git submodule update --init --remote`.

make.bash, make.bat and CI all call it, and `branch = master` in `.gitmodules` is the fallback. A third-party dependency keeps its version.

**A dependency's generators run only for this org.** `cmd/go` completes a fetched module by running its `//go:generate` directives (`cmd/go/internal/gendep`). A directive is a command its author wrote. This machine runs it unread. A module under `github.com/wow-look-at-my/` completes that way, because that author is this fleet. Every other module runs nothing.

It asks with a whole-line `//go:gendep` comment in its own `go.mod`. A skip is named on stderr, not left to surface later as an undeclared symbol. Depth: docs/GENDEP.md.

**A failure is fixed, not concealed.** There is no transient failure. A test that passes on the second run is broken on the first, and re-running it is not a repair. So `-count=1` is a no-op here, and clearing a cache is never the answer to a incorrect result. A cache that serves a incorrect answer has a key missing an input. Name that input and put it in the key.

Do not retry or re-run it. Do not widen a timeout or mark a check flaky. Do not delete state to move past it. Find what is incorrect and repair it.

**`awk` and `sed` are not permitted.** Neither in a script, a workflow step, nor a one-off command. Each carries its own grammar, and that grammar edits what you did not ask it to. `sed -i` takes a mandatory backup suffix on BSD and none on GNU. One invocation cannot mean the same thing on both runners. `awk` rebuilds the whole line out of `OFS` as soon as you assign to a field.

So a version rewrite stripped the leading tab from a couple of `go.mod` require lines, and nothing failed. The shell reads a line and puts back every part of it untouched. Use it, or use the file's own tool.

**A name is a word, not a letter.** Every identifier is a few characters. There is no carve-out for a loop counter: write `idx`, `row`, `col` or `num`. This binds every line this fork adds. Do not rename upstream's receivers in a file you are not otherwise rewriting. That diff hides the change it travels with.

**A module zip carries `.gitmodules` and every gitlink.** `git archive` writes an empty directory for a submodule. It drops the commit that submodule points at. A fetched module then names which submodules exist and not which commit each one is. A gitlink is a content address the author already chose. Keep it.

The GitHub Actions workflow (`.github/workflows/cosmo-ci.yml`) builds the toolchain on Linux, macOS, and Windows and tests that APE binaries built on any platform run correctly. The single `test` job is a 3-OS matrix (ubuntu/macos/windows). Every leg runs the full apetest suite against all origin binaries. The windows-latest leg additionally runs windows-only steps before the shared apetest steps. One is a never-failing AF_UNIX capability diagnostic (attributes any unixsock failure to runner. `fizzbuzz.com 10 5` prints `fizzbuzz\n`, exit 0).

Its apetest steps - fizzbuzz battery AND runtimeprobe execution, via direct CreateProcess - keep the longer per-step timeouts the dedicated windows job used, carried as.

CI builds one fat APE per platform. No GOARCH pin. The output contains cosmo amd64 and cosmo arm64 payloads, stripped by default, with apetest's `TestFatSidecarsExist` asserting the `.dbg`/`.aarch64.elf` sidecars exist on every build. The artifact ships the bare binaries, so apetest's TestDebugSidecars skips on the test runners). Structural format tests run everywhere. The full execution suite (fizzbuzz + runtimeprobe) runs on all test runners.

**Every build leg runs `src/run.bash`** (`run.bat` on windows), which execs `go tool dist test`. There is no `-rebuild` and no `-a`: both existed to force `go install -a` over the whole toolchain, which threw away a correct cache on every run. That is the distribution's own all-tests entry point and the only test gate here: the stdlib and `cmd` packages, the `test/` corpus through `cmd/internal/testdir`. A plain `go test` reaches none of the last two. It replaced a set of steps that each named the tests it wanted, which made green a statement about the name lists rather than. Never reintroduce a `-run` list here: a test nobody names never runs.

**Every step dist runs names itself and what it cost.** `dist build <secs> <name>` and `dist test <secs> <name>`, one line each, on stdout. A suite's own `ok pkg 1.23s` times the RUN of a test binary, not the build of one. The binary every package's tests compile into runs no test at all. Without these lines the largest compile in a leg answers to nothing. `make.bash` gets the same line for each toolchain phase.

The suite-leg summary totals them into the step summary, which is how a leg that overran its budget names what overran. After `std.test` and `cmd-go.test`, `dist action <secs> wait <secs>` lines name the slowest actions of that step from go test's `-debug-actiongraph`. `cmd/go` is its own `cmd-go.test` step, because its scripts each build with `-p`. One binary carries one deadline, and beside `std.test`'s packages its actions waited a minute at a time for a worker. The deadline ran out. It runs after `std.test`, alone.

The `cosmo-checks` job keeps `dats/checks/cosmo-tests.dats`, which runs the GOOS=cosmo package tests through the misc/cosmo wrappers. Internal/runtime/syscall/cosmo (darwin sendmsg/recvmsg cmsg repack, signal translation tables, epoll layout), the runtime's Apple itimerval ABI pins. Those name lists live in the suite, where an engineer can run them, rather than in a workflow step. Every build leg additionally asserts, right after make.bash, a content-derived `buildID=` in `compile -V=full`. That is the cross-build cache-poisoning guard — see the tool-build-ID bullet in Fork Gotchas.

Guard suites, the test programs and the wasm job: docs/CI.md, section "Guard suites, test programs and the wasm job".

The `publish-create`, `publish-upload` and `publish-finish` jobs publish an installable toolchain tarball to buildhost on every push, one leg per platform. They need every other job green: build, cosmo-checks, test, wasm and wasm-suite. Depth: Toolchain Distribution below.
