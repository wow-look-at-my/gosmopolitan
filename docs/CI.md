# CI internals (cosmo-ci.yml)

Depth behind the comments trimmed from `.github/workflows/cosmo-ci.yml` (the `yaml-comment-block` guard caps a workflow file at 1 comment line in a row). See CLAUDE.md's "CI" section for the job overview. This file covers the per-step rationale that overview does not.

## Every job: `submodules: true`

`cmd/go` links the org's shared build cache client in, and the three packages that needs live under `src/cmd/vendor` as **git submodules** rather than copied. `src/cmd` builds in vendor mode, so those paths must hold real files at build time: a checkout without them leaves three empty directories and. Every `actions/checkout` in this workflow therefore passes `submodules: true`, publish jobs included — they check the tree out to package it, and a GOROOT missing `cmd/go`. The same applies to a developer's clone: use `--recurse-submodules`, or `git submodule update --init` afterwards.

## build job

**Timeouts.** Job and step `timeout-minutes` are deliberate everywhere: a hung cosmo binary (or a wedged runner) must never burn GitHub's 6-hour default. Limits are sized ~2x (or a round number above) the slowest duration observed across recent green runs. See the values at each step.

**Build APE binary.** No GOARCH pin: `GOOS=cosmo go build` emits a fat (amd64+arm64) APE regardless of the host architecture. `apetest`'s `TestFatSidecarsExist` (gated on `APE_REQUIRE_SIDECARS=1`, since sidecars are not uploaded and the same suite's bare `go test ./...` runs elsewhere must not fail on their absence) requires the debug sidecars a default fat build must write next to each output, and checks each is a valid. Observed max 38s (windows. Two fat builds = four link passes).

**Build platform-subset APE binaries.** `GOCOSMOPLATFORMS` restricts which hosts the APE boots on. Two subsets, both executed on every test leg (see the test job):

- `tri` - linux/amd64, darwin/arm64, windows/amd64. Still needs both payloads, so it is the same size as the fat build. What it drops is the macOS Intel claim. This is the set consumers ask for, and every one of its three platforms must still boot the binary.
- `amd` - linux/amd64, windows/amd64. One payload: the arm64 image, its boot header and its sidecar are gone, which is where the size actually drops.

Cross-compiles, so one leg builds them for all three to run. `apetest`'s `TestSlimSidecarsExist` (same `APE_REQUIRE_SIDECARS=1` gate, run once per subset with `SLIM_BIN`/`SLIM_PLATFORMS` set) asserts a restricted build still writes a sidecar per payload it carries, and that the amd-only pair has no `.aarch64.elf`.

**GOOS=cosmo package tests (dats).** `dats/cosmo-tests.dats`, run through the org's `wow-look-at-my/dats@master` action. Three commands, each `GOOS=cosmo`-only code executed on this host via the `misc/cosmo` exec wrappers - the test binary is a thin cosmo APE that. The cosmo syscall shim package covers the darwin sendmsg/recvmsg cmsg repack, the signal and wait-status translation tables, and the epoll layout. The runtime commands cover the Apple itimerval ABI pins and the timeval translation behind the darwin SIGPROF setitimer dispatch. The `syscall` command covers the macOS statfs/utsname struct conversions - which live in package `syscall` because the Apple buffers are far past the emulation's nosplit. The runtime command also pins the iphlpapi FIXED_INFO offsets `os_cosmo_nt_dns.go` walks, where a wrong offset reads plausible garbage rather than failing. Two of the three name the tests they cover rather than running the whole package, and a name list is a test-selection decision: it. Observed ~75s warm.

**The whole test suite (run.bash).** Every other test step here used to name the tests it wanted, and a test nobody names never runs. That makes green a statement about the list rather than about the tree, and it is how the parameter-default tests shipped covered by nothing. `run.bash` is the distribution's own answer: it execs `go tool dist test -rebuild`, which is the single call that runs everything - the stdlib and `cmd` packages, the `test/`. A plain `go test` reaches none of the last two.

It runs on every build leg, not one. Upstream runs the same suite on each builder because a port is a host, and this fork's whole subject is code that behaves differently. Windows takes `run.bat`, which is the same `dist test` call.

It runs in short mode. `dist test` reads `GO_BUILDER_NAME`, and a nameless builder gets the short set, which is what upstream's ordinary builders run.

One failure is known and structural: cmd/go's `list_symlink_issue35941` walks `src/cmd/vendor` on disk and cannot resolve the whole-repo submodules' own commands. See CLAUDE.md's vendoring section for why a pruned vendor tree is not available here.

## test job

**Windows execution acceptance overview.** The windows-latest leg is also the cosmo NT execution acceptance: before the shared apetest steps it runs two windows-only steps.

Job-level cap: 3 test steps at their per-OS step timeout plus setup. Observed green totals are well under 2 minutes on every leg. Individual steps carry their own `timeout-minutes` (and an in-step killer, see `with-deadline.sh`). The per-OS knobs ride the matrix: windows keeps the timings the old dedicated test-windows job proved out (40-minute job cap. 10-minute apetest step timeouts. 540s in-step deadlines - inert there, since `with-deadline.sh` self-detects git-bash and degrades to a plain exec, leaving `timeout-minutes` as the watchdog). The unix legs keep 25/5/240.

**AF_UNIX capability probe / Run fizzbuzz on Windows.** A normal fat APE - cosmo amd64 + arm64, no embedded second build - boots natively on Windows through its. Two windows-only rungs before the shared apetest steps:

1. Direct invocation (ubuntu- and windows-origin artifacts. All origins build identical headers): the same contract apetest asserts (`fizzbuzz.com <a> <b>` prints `fizzbuzz(a+b)` + `\n` on stdout, exit 0), with a byte-for-byte stdout compare.
2. The apetest suite, natively, against all three origin binaries with `FIZZBUZZ_BIN` and `RUNTIMEPROBE_BIN` both set: NT bring-up wave 2 grew the runtime surface runtimeprobe.

The AF_UNIX probe is diagnostic only (never fails the job): it proves what the runner's `afunix.sys` actually supports, so a runtimeprobe unixsock failure can be attributed - runner. The native matrix reproduces the cosmo runtime's exact socket recipe piecewise (creation flags, `SO_REUSEADDR` - which net's `listenStream` sets and which poisons a subsequent afunix bind with `WSAEOPNOTSUPP` - and `FIONBIO`). The managed .NET case is the canonical known-good control.

The fizzbuzz NT boot check runs binaries via `Start-Process` rather than pwsh native invocation (`& $copy`): on one observed run both binaries printed the correct. `Start-Process` + `$p.ExitCode` isolates the check from that teardown path, and the script ends in an explicit exit so the host quits before whatever teardown. Per-binary lines before and after each run localize any future hang. Cases restate apetest/fizzbuzz_test.go's invocation contract: `fizzbuzz.com <num1> <num2>` prints `fizzbuzz(num1+num2)` and a trailing `\n` (`fmt.Println`), exit 0. `TestFizzbuzz_15` (10+5) wants "fizzbuzz"; `TestNumber_13` (7+6) wants "13" - the second case proves argv values actually flow through `GetCommandLineW` parsing rather than printing a constant. Each origin runs a throwaway copy so the downloaded artifact stays pristine (APE binaries self-assimilate on unix hosts. Keep the same hygiene here).

**Test binary built on \<OS\> steps.** Each test step runs `go test` under an in-step process-group killer (see `with-deadline.sh`): runner-side step timeouts have been observed not. One wedged process even survived a process-group SIGKILL, i.e. it was stuck in an uninterruptible kernel state). The killer abandons such a corpse so the step still ends, and `go test`'s output goes through a file (`cat`'ed afterwards) so no abandoned descendant.

**Upload \<origin\> test log steps.** Uploaded immediately: job-level logs only reach the API when the whole job ends, which a wedged later step (or.

**Test platform-subset APEs.** A platform-subset APE must boot and run on every platform it still claims, on the real host - linking is not evidence. Both subsets run here, on all three legs. Each one's `TestSlimRuns` skips itself on a host it deliberately dropped (amd on macOS), while the structural checks - which payload, which boot header, which loader pieces. `-run` selects the execution battery plus `TestSlim*`: the rest of the suite (`TestELF*`, `TestMacho*`, `TestPE*`, `TestFat*`, `TestShell*`) pins the shape of an UNRESTRICTED build and is asserted against. The subset's own shape is what `TestSlim*` asserts, parameterized by `SLIM_PLATFORMS`.

## wasm job

Regression-gates the fork's WebAssembly ports (`GOOS=js` and `GOOS=wasip1`). See docs/WASM.md for every round's measurements and gates, and WASM_SHORTCOMINGS.md for the catalog of fixes. Wasm output is host-independent, so a single ubuntu leg is enough. Job cap is a backstop only: a hung step trips its own `timeout-minutes` first, and later steps are skipped on failure. Observed green total is well under 15 minutes.

`wasm_exec_node.js` (the js/wasm exec wrapper in `lib/wasm`) requires node >= 18. Wazero installs before the fork toolchain is built so `go` is unambiguously the bootstrap from setup-go: the fork's `bin/go` defaults `GOOS=cosmo`, so a bare. GOOS/GOARCH are pinned anyway, belt and braces. Observed 23s locally, including the go1.25 toolchain auto-download the wazero module demands.

**Test the wasm object writer.** Runs on the HOST - it shells out to `go tool compile -S` for both wasm ports - so it needs an explicit `GOOS=linux`. Pins the branch lowering: a forward-only function must emit no `PC_B` store, and a loop must still emit one for its backedge.

**Test streaming fetch uploads under node (jsfetchstream e2e).** End-to-end proof that `GODEBUG=jsfetchnode=1` request bodies with unknown length STREAM: the guest writes chunk A, waits until. Also covers the known-length flip side (buffered, Content-Length preserved), an 8 MiB bounded-memory upload, and mid-stream context cancellation (prompt return, body closed, server sees the abort).

**Test GOWASI=wasmedgesock.** End-to-end tests for the WasmEdge socket extension: the host module embeds wazero and builds every guest program with the fork toolchain (`bin/go`). The first guest build populates the `GOWASI=wasmedgesock` build cache.

**Test GOWASM=threads under node.** Modules import a shared linear memory, use the 0xFE atomic instructions, carry passive data segments plus a linker-emitted `_initmem` export. Node-only: wazero and wasmtime lack the threads proposal, so wasip1 rejects the flag at link time (also asserted here). The pool demo hammers a shared counter from 4 worker instances. The thread demo runs Go goroutines on three Ms across three threads (looped 10x to shake out races). The pool_demo gate needs `pipefail` to propagate node's exit code through `tee`, the PASS line must be present, and no runtime fatal may hide. The demos print via `println` (stderr - any goroutine can land on a worker M, where fmt/os.Stdout is unavailable), so the gates capture stderr with `pipefail` set. The cross-thread grow-observation gate has pinned worker Ms hammer atomics on chunks another thread just grew the memory for. Without the assembler's grow-observation guard this traps with "memory access out of bounds" (the nondeterministic worker crash at `runtime.newMarkBits`).

**Compiler regression tests (testdir wasmexport).** `GOOS=linux` pin: the harness test binary runs on the host (the fork's `go` can otherwise target cosmo); `-target` selects the wasm port under test.

## publish jobs

Publishes an installable toolchain distribution to buildhost (pazer.build) on every push, once build+test are green. See CLAUDE.md's "Toolchain Distribution" and docs/INSTALL.md for the consumer side. Auth is a GitHub Actions OIDC token (audience `https://pazer.build`). The buildhost project auto-provisions on the first authenticated push, and every branch gets its own rolling latest (`?branch=<name>`).

Publishing is three jobs so each platform's tarball is built ON that platform (distpack packages what a HOST build produced, so `GOOS=darwin GOARCH=arm64 ./make.bash -distpack` fails with "distpack: stat bin/darwin_arm64/go: no such file or directory" - there is no cross-package shortcut):

- `publish-create` - one release, so every platform lands in ONE version.
- `publish-upload` - per-platform build + upload, straight to buildhost.
- `publish-finish` - publish that release once every platform is in.

Nothing is handed between the jobs: each leg builds its own tarball on its own runner and uploads it directly, so no GitHub artifact. A failed leg means `publish-finish` never runs and the release stays a draft, which buildhost records as INTENT and never serves as latest - so.

**Create buildhost release.** No version input: buildhost auto-increments the project version. `git_branch`/`git_commit` default to the pushed branch and sha inside the action, so every branch keeps its own rolling latest and branch pushes never.

**Stamp unique per-release version.** The fork identifies as a RELEASE Go version, so cmd/go derives tool IDs (hence action IDs) from the version string alone. Two releases sharing one string share a build-cache namespace, and the org's shared build cache then links objects from different releases into one binary. A unique monotonic suffix per publish makes each release's cache namespace disjoint. The committed VERSION is unchanged. This rewrite is publish-only. Every leg stamps the SAME string: `run_number` is one value per run, so the platforms of one release cannot disagree about which toolchain they.

**Build distribution archive.** A host build plus distpack packaging: writes the official-style `go<VERSION>.<goos>-<goarch>.tar.gz` to `pkg/distpack`, on every platform. `make.bash` alone is ~2m30s on ubuntu, ~4m30s on macos and ~3m15s on windows (`make.bat`, selected by the multicmd action). Distpack adds only seconds.

Upstream distpack writes a `.zip` for windows INSTEAD of the tar, off the one `zipArch` both containers hold. The fork writes the tar there too and drops the zip. buildhost stores one blob per os/arch and repackages it at download time, so. A second container written at build time is a copy nothing fetches.

A GOROOT is why an archive is uploaded at all: buildhost's stored unit is one file, and `bin/`, `pkg/`, `src/` and `lib/` cannot be. That is unlike every other project here, which uploads the binary itself.

**Publish flow.** Uses buildhost's own composite actions (create-release -> upload-artifact -> publish-release) - the same family the rest of the org uses - instead of REST calls hand-rolled in this. Each action mints its own OIDC token (audience = the server URL).
