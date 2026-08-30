# CI internals (cosmo-ci.yml)

Depth behind the comments trimmed from `.github/workflows/cosmo-ci.yml` (the
`yaml-comment-block` guard caps a workflow file at 1 comment line in a row).
See CLAUDE.md's "CI" section for the job overview; this file covers the
per-step rationale that overview doesn't.

## build job

**Timeouts.** Job and step `timeout-minutes` are deliberate everywhere: a
hung cosmo binary (or a wedged runner) must never burn GitHub's 6-hour
default. Limits are sized ~2x (or a round number above) the slowest duration
observed across recent green runs; see the values at each step.

**Build APE binary.** No GOARCH pin: `GOOS=cosmo go build` emits a fat
(amd64+arm64) APE regardless of the host architecture. `apetest`'s
`TestFatSidecarsExist` (gated on `APE_REQUIRE_SIDECARS=1`, since sidecars
are not uploaded and the same suite's bare `go test ./...` runs elsewhere
must not fail on their absence) requires the debug sidecars a default fat
build must write next to each output, and checks each is a valid ELF for
its architecture -- run right here, before upload, since the sidecars never
leave this runner. Observed max 38s (windows; two fat builds = four link
passes).

**Build platform-subset APE binaries.** `GOCOSMOPLATFORMS` restricts which
hosts the APE boots on. Two subsets, both executed on every test leg (see
the test job):

- `tri` - linux/amd64, darwin/arm64, windows/amd64. Still needs both
  payloads, so it is the same size as the fat build; what it drops is the
  macOS Intel claim. This is the set consumers ask for, and every one of
  its three platforms must still boot the binary.
- `amd` - linux/amd64, windows/amd64. One payload: the arm64 image, its
  boot header and its sidecar are gone, which is where the size actually
  drops.

Cross-compiles, so one leg builds them for all three to run. `apetest`'s
`TestSlimSidecarsExist` (same `APE_REQUIRE_SIDECARS=1` gate, run once per
subset with `SLIM_BIN`/`SLIM_PLATFORMS` set) asserts a restricted build
still writes a sidecar per payload it carries, and that the amd-only pair
has no `.aarch64.elf`.

**APE merge unit tests (linker + go command).** Covers the
`-apefat`/`-apestrip`/`-apedbg` merge logic and cmd/go's `GOCOSMOSTRIP` /
`-ldflags` strip detection. One leg is enough. `GOOS=linux` pin: the fork's
`go` defaults `GOOS=cosmo`, and these are host-run unit tests. Observed
~40s (test-package compile). The platform table both commands parse
`GOCOSMOPLATFORMS` and `-apeplatforms` through, and the `go env` readouts a
consumer probes to tell an aware toolchain from one that ignores the
selection, is load-bearing outside this repo, so it's gated here. The cosmo
syscall package's unit tests (darwin sendmsg/recvmsg cmsg repack,
signal/wait-status translation tables, epoll layout) are `GOOS=cosmo`-only
code, run on this host via the `misc/cosmo` exec wrappers - the test binary
is a thin cosmo APE that executes natively on linux. Runtime-package cosmo
unit tests cover the Apple itimerval ABI pins + timeval translation behind
the darwin SIGPROF setitimer dispatch, and the signal translation tables.

**Fork-divergence guardrails (go/build policy, moddeps, distpack naming).** go/build's
structural tests are what keep a fork's edits honest across an upstream
uprev, and nothing was running them: the shebang step only runs `-run
TestReadGoInfo`. Both had silently gone red - `TestVendorPackages` against
the zstd vendored for cosmo DWARF compression, `TestDependencies` against
`internal/runtime/syscall/cosmo` - and an uprev is exactly when a stale
dependency policy stops catching real layering breaks. `cmd/internal/moddeps`
is the matching check for `src/` + `src/cmd` module/vendor consistency (see
CLAUDE.md's vendoring runbook). `cmd/distpack` joins them because the publish
legs upload by exact filename: see "publish jobs" below.

**Host-nameserver path (net) and the NT DNS ABI pins (runtime).** An NT host
publishes its resolvers where `net` cannot open them, so a cosmo binary there
took `dnsconfig_unix.go`'s missing-file fallback and asked localhost - see
DEBUGGING.md's off-host HTTPS section. The `net` half of this step covers the
seam that replaced that fallback; the `runtime` half pins the iphlpapi
FIXED_INFO offsets `os_cosmo_nt_dns.go` walks, which only a Windows host ever
executes and where a wrong offset reads plausible garbage rather than failing.
The end-to-end resolve is runtimeprobe's `dns` check, which every test runner
executes.

**Loop-aware inlining tests.** See docs/LOOP-INLINING.md for the loop cost
discount, the loop-nested call site budget, and the per-caller growth
allowance. Host-run compiler tests, so GOOS is pinned away from cosmo. The
inlining regress tests assert that more inlining does not break the
existing expectations for what does and does not get inlined.

## test job

**Windows execution acceptance overview.** The windows-latest leg is also
the cosmo NT execution acceptance: before the shared apetest steps it runs
two windows-only steps (AF_UNIX runner diagnostic, direct fizzbuzz NT boot
check).

Job-level cap: 3 test steps at their per-OS step timeout plus setup;
observed green totals are well under 2 minutes on every leg. Individual
steps carry their own `timeout-minutes` (and an in-step killer, see
`with-deadline.sh`). The per-OS knobs ride the matrix: windows keeps the
timings the old dedicated test-windows job proved out (40-minute job cap;
10-minute apetest step timeouts; 540s in-step deadlines - inert there,
since `with-deadline.sh` self-detects git-bash and degrades to a plain
exec, leaving `timeout-minutes` as the watchdog); the unix legs keep
25/5/240.

**AF_UNIX capability probe / Run fizzbuzz on Windows.** A normal fat APE -
cosmo amd64 + arm64, no embedded second build - boots natively on Windows
through its PE header and the cosmo runtime's NT personality. Two
windows-only rungs before the shared apetest steps:

1. Direct invocation (ubuntu- and windows-origin artifacts; all origins
   build identical headers): the same contract apetest asserts
   (`fizzbuzz.com <a> <b>` prints `fizzbuzz(a+b)` + `\n` on stdout, exit 0),
   with a byte-for-byte stdout compare.
2. The apetest suite, natively, against all three origin binaries with
   `FIZZBUZZ_BIN` and `RUNTIMEPROBE_BIN` both set: NT bring-up wave 2 grew
   the runtime surface runtimeprobe needs (file I/O, dirents, TCP/UDP/unix
   sockets, signals, os/exec, timers, async preemption), so probe execution
   is gated here too.

The AF_UNIX probe is diagnostic only (never fails the job): it proves what
the runner's `afunix.sys` actually supports, so a runtimeprobe unixsock
failure can be attributed - runner capability gap vs cosmo port bug. The
native matrix reproduces the cosmo runtime's exact socket recipe piecewise
(creation flags, `SO_REUSEADDR` - which net's `listenStream` sets and which
poisons a subsequent afunix bind with `WSAEOPNOTSUPP` - and `FIONBIO`); the
managed .NET case is the canonical known-good control.

The fizzbuzz NT boot check runs binaries via `Start-Process` rather than
pwsh native invocation (`& $copy`): on one observed run both binaries
printed the correct exit code, yet the pwsh host then sat ~18s past the
final success line and died with exit code 1 and zero diagnostics - a
native-command/console teardown artifact, not an assertion failure.
`Start-Process` + `$p.ExitCode` isolates the check from that teardown path,
and the script ends in an explicit exit so the host quits before whatever
teardown threw. Per-binary lines before and after each run localize any
future hang. Cases restate apetest/fizzbuzz_test.go's invocation contract:
`fizzbuzz.com <num1> <num2>` prints `fizzbuzz(num1+num2)` and a trailing
`\n` (`fmt.Println`), exit 0. `TestFizzbuzz_15` (10+5) wants "fizzbuzz";
`TestNumber_13` (7+6) wants "13" - the second case proves argv values
actually flow through `GetCommandLineW` parsing rather than printing a
constant. Each origin runs a throwaway copy so the downloaded artifact
stays pristine (APE binaries self-assimilate on unix hosts; keep the same
hygiene here).

**Test binary built on \<OS\> steps.** Each test step runs `go test` under
an in-step process-group killer (see `with-deadline.sh`): runner-side step
timeouts have been observed not to fire when a step wedges on the macOS
runners (jobs sat 30+ minutes past `timeout-minutes` and uploaded no logs;
one wedged process even survived a process-group SIGKILL, i.e. it was stuck
in an uninterruptible kernel state). The killer abandons such a corpse so
the step still ends, and `go test`'s output goes through a file (`cat`'ed
afterwards) so no abandoned descendant can keep the runner's log pipe open.

**Upload \<origin\> test log steps.** Uploaded immediately: job-level logs
only reach the API when the whole job ends, which a wedged later step (or a
runner poisoned by an unkillable corpse) can prevent forever.

**Test platform-subset APEs.** A platform-subset APE must boot and run on
every platform it still claims, on the real host - linking is not evidence.
Both subsets run here, on all three legs; each one's `TestSlimRuns` skips
itself on a host it deliberately dropped (amd on macOS), while the
structural checks - which payload, which boot header, which loader pieces,
and the size against the unrestricted build - run everywhere. `-run`
selects the execution battery plus `TestSlim*`: the rest of the suite
(`TestELF*`, `TestMacho*`, `TestPE*`, `TestFat*`, `TestShell*`) pins the
shape of an UNRESTRICTED build and is asserted against the fat binaries in
the steps above. The subset's own shape is what `TestSlim*` asserts,
parameterized by `SLIM_PLATFORMS`.

## wasm job

Regression-gates the fork's WebAssembly ports (`GOOS=js` and `GOOS=wasip1`);
see docs/WASM.md for every round's measurements and gates, and
WASM_SHORTCOMINGS.md for the catalog of fixes. Wasm output is
host-independent, so a single ubuntu leg is enough. Job cap is a backstop
only: a hung step trips its own `timeout-minutes` first, and later steps
are skipped on failure. Observed green total is well under 15 minutes.

`wasm_exec_node.js` (the js/wasm exec wrapper in `lib/wasm`) requires node
>= 18. Wazero installs before the fork toolchain is built so `go` is
unambiguously the bootstrap from setup-go: the fork's `bin/go` defaults
`GOOS=cosmo`, so a bare `go install` with it would cross-compile. GOOS/GOARCH
are pinned anyway, belt and braces. Observed 23s locally, including the
go1.25 toolchain auto-download the wazero module demands.

**Test the wasm object writer.** Runs on the HOST - it shells out to `go
tool compile -S` for both wasm ports - so it needs an explicit
`GOOS=linux`: this tree targets cosmo by default and the test binary would
not exec. Pins the branch lowering: a forward-only function must emit no
`PC_B` store, and a loop must still emit one for its backedge.

**Test streaming fetch uploads under node (jsfetchstream e2e).** End-to-end
proof that `GODEBUG=jsfetchnode=1` request bodies with unknown length
STREAM: the guest writes chunk A, waits until the node server reports A
arrived while the POST body is still open, then writes chunk B - impossible
with a buffered upload. Also covers the known-length flip side (buffered,
Content-Length preserved), an 8 MiB bounded-memory upload, and mid-stream
context cancellation (prompt return, body closed, server sees the abort).

**Test GOWASI=wasmedgesock.** End-to-end tests for the WasmEdge socket
extension: the host module embeds wazero and builds every guest program
with the fork toolchain (`bin/go`), covering the TCP battery (echo, http
both directions, refused, deadlines) and the UDP paths
(udpecho/udpconnected). The first guest build populates the
`GOWASI=wasmedgesock` build cache.

**Test GOWASM=threads under node.** Modules import a shared linear memory,
use the 0xFE atomic instructions, carry passive data segments plus a
linker-emitted `_initmem` export so worker instances can share the memory
without clobbering it, and run real Go code on worker threads: futex
locks/notes over `memory.atomic.wait32/notify`, `newosproc` handing new Ms
to the worker pool `wasm_exec_node.js` pre-spawns. Node-only: wazero and
wasmtime lack the threads proposal, so wasip1 rejects the flag at link time
(also asserted here). The pool demo hammers a shared counter from 4 worker
instances; the thread demo runs Go goroutines on three Ms across three
threads (looped 10x to shake out races). The pool_demo gate needs
`pipefail` to propagate node's exit code through `tee`, the PASS line must
be present, and no runtime fatal may hide anywhere in the output - a
boot-time "fatal error: newosproc: no wasm worker thread available" used to
exit 0 here with none of the demo's payload run (see DEBUGGING.md,
pool_demo silent-fatal). The demos print via `println` (stderr - any
goroutine can land on a worker M, where fmt/os.Stdout is unavailable), so
the gates capture stderr with `pipefail` set. The cross-thread
grow-observation gate has pinned worker Ms hammer atomics on chunks another
thread just grew the memory for; without the assembler's grow-observation
guard this traps with "memory access out of bounds" (the nondeterministic
worker crash at `runtime.newMarkBits`).

**Compiler regression tests (testdir wasmexport).** `GOOS=linux` pin: the
harness test binary runs on the host (the fork's `go` would otherwise
target cosmo); `-target` selects the wasm port under test.

## publish jobs

Publishes an installable toolchain distribution to buildhost (pazer.build)
on every push, once build+test are green; see CLAUDE.md's "Toolchain
Distribution" and docs/INSTALL.md for the consumer side. Auth is a GitHub
Actions OIDC token (audience `https://pazer.build`); the buildhost project
auto-provisions on the first authenticated push, and every branch gets its
own rolling latest (`?branch=<name>`).

Publishing is three jobs so each platform's tarball is built ON that
platform (distpack packages what a HOST build produced, so `GOOS=darwin
GOARCH=arm64 ./make.bash -distpack` fails with "distpack: stat
bin/darwin_arm64/go: no such file or directory" - there is no cross-package
shortcut):

- `publish-create` - one release, so every platform lands in ONE version.
- `publish-upload` - per-platform build + upload, straight to buildhost.
- `publish-finish` - publish that release once every platform is in.

Nothing is handed between the jobs: each leg builds its own tarball on its
own runner and uploads it directly, so no GitHub artifact storage is
involved. A failed leg means `publish-finish` never runs and the release
stays a draft, which buildhost records as INTENT and never serves as
latest - so a half-uploaded release cannot be installed.

**Create buildhost release.** No version input: buildhost auto-increments
the project version. `git_branch`/`git_commit` default to the pushed
branch and sha inside the action, so every branch keeps its own rolling
latest and branch pushes never move `?branch=master`.

**Stamp unique per-release version.** The fork identifies as a RELEASE Go
version, so cmd/go derives tool IDs (hence action IDs) from the version
string alone. Two releases sharing one string share a build-cache
namespace, and the org's shared GOCACHEPROG cache then links objects from
different releases into one binary. A unique monotonic suffix per publish
makes each release's cache namespace disjoint. The committed VERSION is
unchanged; this rewrite is publish-only. Every leg stamps the SAME string:
`run_number` is one value per run, so the platforms of one release cannot
disagree about which toolchain they are.

**Build distribution archive.** A host build plus distpack packaging:
writes the official-style `go<VERSION>.<goos>-<goarch>.tar.gz` to
`pkg/distpack`, on every platform. `make.bash` alone is ~2m30s on ubuntu,
~4m30s on macos and ~3m15s on windows (`make.bat`, selected by the multicmd
action); distpack adds only seconds.

Upstream distpack writes a `.zip` for windows INSTEAD of the tar, off the
one `zipArch` both containers hold. The fork writes both, and the leg
uploads the tarball: a consumer that already extracts a gzipped tar for two
platforms would otherwise need a second extractor for the third, and
go-toolchain's cosmo bootstrap is that consumer. `binaryDistNames`
(`src/cmd/distpack/pack.go`) is the selection, pinned by
`TestBinaryDistNames` and run in the build job's guardrails step -- the
publish uploads by exact name, so a change here breaks a platform's
download rather than the build, which is the failure a unit test is cheap
insurance against.

**Publish flow.** Uses buildhost's own composite actions (create-release ->
upload-artifact -> publish-release) - the same family the rest of the org
uses - instead of REST calls hand-rolled in this workflow. Each action
mints its own OIDC token (audience = the server URL).
