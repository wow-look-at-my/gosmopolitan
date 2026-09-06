# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Don't Ask Stupid Questions

When there is a specification, **follow the specification**. Never ask "must I follow the spec or do something different?" - the answer is always follow the spec. That is what specs are for. If the implementation does not match the spec, fix the implementation.

## Project Overview

This is a fork of the Go programming language toolchain that adds support for **Cosmopolitan Libc** (`GOOS=cosmo`). Cosmopolitan enables building "Actually Portable Executables" (APE) - single binaries that run natively on Linux, macOS, and Windows without modification.

## No Rosetta Dependency

**APE binaries run natively on all platforms without emulation.** This is not a goal or theory - it is proven, working technology. Real APE executables (like `vim.com` from Cosmopolitan) already run natively on x86_64 Linux, x86_64 macOS, ARM64 macOS, and Windows today without Rosetta. This fork's own output executes on Linux, macOS, and (since the cosmo-native NT bring-up, wave 1) Windows - see Building Cosmopolitan Binaries below for the per-platform status.

- APE binaries contain native code for multiple architectures (AMD64 + ARM64)
- On ARM64 macOS, APE runs native ARM64 code - NOT x86_64 via Rosetta
- The cosmocc toolchain does not need Rosetta, neither do we
- If something "works via Rosetta". That is not actually working - it is a bug

When building/testing:
- `GOARCH=amd64` produces x86_64 code only - will NOT work natively on ARM64 macOS
- Full APE support requires both AMD64 and ARM64 code paths
- Runtime must detect host OS/arch and use appropriate syscall method

**macOS syscall restriction**: macOS (both x86_64 and ARM64) does not allow raw syscalls. All syscalls must go through Apple's frameworks via the `syslib` function pointer table. Code that uses raw `SYSCALL`/`SVC` instructions will crash with SIGSYS on macOS.

## Build Commands

Build from the `src/` directory. Requires a Go 1.24+ bootstrap toolchain (set `GOROOT_BOOTSTRAP` or have `go` in PATH).

```bash
# Build the toolchain (Unix)
cd src && ./make.bash

# Build the toolchain (Windows)
cd src && make.bat

# Build and run all tests
cd src && ./all.bash

# Run tests only (after build)
cd src && ./run.bash
```

## Testing

```bash
# Run all tests in test/ directory
go test cmd/internal/testdir

# Run specific test files
go test cmd/internal/testdir -run='Test/(file1.go|file2.go)'

# Run compiler package tests
go test ./cmd/compile/...

# Run standard library tests
go test std
```

To run tests under GOOS=cosmo on a Linux/macOS host, `export PATH="$GOROOT/misc/cosmo:$PATH"` so cmd/go finds the `go_cosmo_*_exec` wrappers (see `misc/cosmo/README.md`). Then plain `GOOS=cosmo go test <pkg>` works.

**Top-level tests are parallel by default in this fork** (`src/testing`): each starts as if it had called `t.Parallel()`, which is a no-op there. A SUBTEST is not, and runs inside the `t.Run` call that starts it, so a parent's later statements and its deferred calls come after it. That is the order upstream promises and the order test code relies on. A subtest asks for parallelism with `t.Parallel()`. Two methods opt a top-level test out. `t.Serial()` stops every other test and runs the caller alone in this process.`t.Fork()` runs the caller in a child process instead, alone, and. `t.Setenv` and `t.Chdir` fork rather than take the barrier: neither is a reason to stop the suite. A test failing only under this fork's `go test` is almost always one of these. Depth: docs/TESTING-PARALLEL.md - both methods, when to pick which, and the fork's mechanics.

## Building Cosmopolitan Binaries

```bash
# Build a fat (amd64+arm64) APE binary - GOARCH is ignored for the output;
# go build always builds both architectures and merges them. The shipped
# APE is stripped (no DWARF/symtab, cosmocc-style); full debug info lands
# in two sidecar ELFs next to it: program.com.dbg (cosmo amd64) and
# program.com.aarch64.elf (cosmo arm64).
GOOS=cosmo go build -o program.com main.go

# go install produces the same fat APE + sidecars in the install directory
GOOS=cosmo go install ./cmd/program

# Keep full debug info embedded in the APE and write no sidecars
# (pre-2026-07-18 behavior, byte-for-byte)
GOCOSMOSTRIP=0 GOOS=cosmo go build -o program.com main.go

# Debug-info tier (GOCOSMODEBUG; unset/full = pristine runnable sidecars,
# today's default). slim: debug-only sidecars, ~-68%, same names, not
# runnable - gdb/delve consume them unchanged, full fidelity. min: slim's
# sidecar shape PLUS less DWARF generated in the first place (no location
# lists, no inline records - injected gcflags, ~-38% below slim):
# breakpoints and file:line backtraces stay exact, but argument/local
# VALUES in debuggers are garbage or <optimized out> - "backtraces yes,
# variables no". compact: slim sidecars PLUS line-level debug info
# appended to the APE past the load span (never mapped; ~+38% APE size) -
# gdb gets file:line backtraces from the assimilated .com alone, no
# sidecar present. Invalid values fail any cosmo build. GOCOSMOSTRIP=0
# or -ldflags -s/-w suppress sidecars (nothing to shape; min's
# compile-time DWARF trim still applies). See DEBUGGING.md.
GOCOSMODEBUG=slim GOOS=cosmo go build -o program.com main.go
GOCOSMODEBUG=min GOOS=cosmo go build -o program.com main.go
GOCOSMODEBUG=compact GOOS=cosmo go build -o program.com main.go

# Explicit -s/-w wins: user-stripped APE, no sidecars (nothing to put in them)
GOOS=cosmo go build -ldflags="-s -w" -o program.com main.go

# Opt out of the fat build (single-architecture APE for the current GOARCH;
# thin builds never strip and get no sidecars)
GOCOSMOFAT=0 GOOS=cosmo GOARCH=amd64 go build -o program.com main.go

# Restrict which hosts the APE boots on. Tokens: linux/amd64, linux/arm64,
# darwin/amd64, darwin/arm64, windows/amd64 (which boots the AMD64 payload
# through the PE header - there is no windows payload). UNSET selects the
# three supported platforms, linux/amd64 + darwin/arm64 + windows/amd64, not
# all five: linux/arm64 and darwin/amd64 stay selectable but a default build
# does not claim them, because nothing verifies either: there is no runner
# for linux/arm64 or darwin/amd64. An unknown token, an
# empty list, or a platform whose payload is missing fails the build; a
# selection that needs one architecture skips the sibling build entirely and
# is still stripped and given its sidecar. NOT a size win by itself: the APE
# header is a fixed 64K, so only dropping an ARCHITECTURE changes the size
# (-47% for amd64-only), and the default needs both payloads and weighs
# exactly what the fat APE does. What a subset buys is an accurate claim and
# a host refused by name. `go env GOCOSMOPLATFORMS` reports
# the effective selection, which is how a consumer detects support for it.
# Depth, including the per-platform payload/header table: DEBUGGING.md
# "Platform-subset APEs".
GOCOSMOPLATFORMS=linux/amd64,darwin/arm64,windows/amd64 GOOS=cosmo go build -o program.com main.go

# The same selection at the linker, over already-built payloads (one or two)
go tool link -apefat amd64.com,arm64.com -apeplatforms linux/amd64,darwin/arm64 -o program.com -apestrip -apedbg

# Merge two single-arch cosmo binaries into one fat APE by hand
# (-apestrip -apedbg is what go build passes by default; omit them for a
# full-payload merge)
go tool link -apefat amd64.com,arm64.com -o program.com -apestrip -apedbg
```

Fat-build coverage: `go build` (with or without `-o`. A plain single-main-package build defaults its output name and fattens too) and `go install` both produce fat APEs. `go test` / `go test -c` binaries stay thin on purpose: they are host-run throwaway artifacts executed right here (via the `misc/cosmo` wrappers), and fattening can triple every test compile.

The fat build's own mechanics - the concurrent sibling build and `GOCOSMOFATSEQ`, the strip-and-sidecar default and the sidecar names, and what each `GOCOSMODEBUG` tier trades.

Shipping APEs: distribute release binaries zstd-compressed - the two arch payloads make APE images highly redundant, so the wire cost collapses. Measured on a stdlib-heavy webserver (net/http, crypto/tls, image/png, time/tzdata): 17.3 MB unstripped fat, 12.3 MB stripped default, and 3.6 MB on the wire after `zstd -19 --long=27`. Distribution-side only, by design: there is no runtime self-extraction mechanism. buildhost can repackage uploaded artifacts on the fly via its `fmt=` query parameter.

The resulting `.com` file runs on Linux, macOS, and Windows. The cosmo amd64 image boots on x86-64 Linux (staged copy). The cosmo arm64 image boots on ARM64 Linux (installed `ape` loader, else a staged copy) and ARM64 macOS (compiled APE loader, no Rosetta). On Windows the SAME cosmo amd64 image boots natively through the APE's PE header (vim.com-style, no embedded second build - the windows/amd64 PE payload was.

Per-platform runtime status - what works today on each host an APE boots on, what is still missing, and the forensics behind each: docs/PLATFORM-STATUS.md. In short: Linux amd64/arm64 complete. Windows amd64 complete through NT bring-up wave 3 plus the 2026-09-02 metadata syscalls (chtimes/truncate/fchdir/link. Still missing: file/pipe dup(2), off-host TCP coverage - DNS is resolved from iphlpapi and probed on every runner). Windows/arm64 has its Win32 layer as of 2026-09-02 - AAPCS64 ntcall trampolines, ARM64_NT_CONTEXT, VEH thunks - but no boot path (the APE has no arm64 PE. MacOS arm64 complete including signals, SIGPROF profiling, SCM_RIGHTS fd passing and (2026-09-02) the file metadata and system-information syscalls - statfs/uname/rlimit/chtimes/priority and the rest. The few Apple cannot serve are listed in docs/STUBS-INVENTORY.md section 6. macOS Intel's SYSCALL surface is complete as of 2026-09-02 (metadata, errno convention and. There is no Intel-mac runner. Nothing there has ever executed - do not claim it works. It is deliberately absent from the default GOCOSMOPLATFORMS set for that reason.

**Variadic libc calls must pass their variadic arguments on the STACK.** arm64-apple diverges from AAPCS64 here, so a variadic callee handed its argument in. Use `runtime.cosmoLibcCallVariadic1` / `darwin_call_v3` for fcntl, open/openat with a mode, and ioctl. Never `cosmoLibcCall6` or `darwin_call`. The runtimeprobe `cloexec` check gates it.

## Architecture

### Compiler Pipeline (`src/cmd/compile/`)

The compiler has 7 phases:
1. **Parsing** (`internal/syntax`) - lexer, parser, syntax tree
2. **Type checking** (`internal/types2`) - type analysis
3. **IR construction** (`internal/ir`, `internal/noder`) - convert to compiler AST
4. **Middle end** (`internal/inline`, `internal/escape`) - inlining, escape analysis
5. **Walk** (`internal/walk`) - desugar high-level constructs
6. **SSA** (`internal/ssa`, `internal/ssagen`) - optimization passes
7. **Code generation** (`cmd/internal/obj`) - machine code output

### Cosmopolitan-Specific Code

Runtime support for `GOOS=cosmo` is in `src/runtime/`:
- `os_cosmo.go` - main OS interface (thread creation, signals)
- `mem_cosmo.go` - memory management
- `netpoll_cosmo.go` - network polling
- `signal_cosmo_amd64.go` - signal handling
- `defs_cosmo_amd64.go` - system constants

Syscall layer: `src/internal/runtime/syscall/cosmo/`

### Key Directories

- `src/cmd/` - toolchain commands (compile, link, go, etc.)
- `src/runtime/` - Go runtime implementation
- `src/` - standard library packages
- `test/` - compiler and runtime test suite

## Debugging Tips

```bash
# View optimization info (inlining, escape analysis)
go build -gcflags=-m=2

# Generate SSA visualization for a function
GOSSAFUNC=FunctionName go build

# Print assembly output
go build -gcflags=-S

# Compiler timing
go tool compile -bench=out.txt file.go
```

## Fork Gotchas

- **This toolchain defaults to `GOOS=cosmo`.** Any `go build`/`go install`/`go test` run with the fork's `bin/go` targets cosmo unless you pin GOOS. Rebuilding a host tool needs e.g. `GOOS=linux GOARCH=amd64 go install cmd/link`, and test harnesses (like `testdata/ape/apetest`) must be run with an upstream Go so the test binary itself is executable on the host.
- **An APE never writes to itself.** The kernel cannot exec the file as it stands, so the bootstrap script stages a copy under `${TMPDIR:-${HOME:-/tmp}}/.ape-run-1/<file identity>/`. The APE keeps its bytes and its checksum, runs from a read-only path, and stays fat. As root, staging also registers the magic with binfmt_misc and binds the copy over the original path in a private namespace. See `docs/APE-STAGING.md`.
- **Tool build IDs are content-derived (2026-07-20).** Upstream derives release-toolchain tool IDs from the tools' `-V=full` version line. The fork stamps the same release-style version (`go1.27.0cosmo`) into every build, so any two fork builds used to share tool IDs — and hence action. Fork tools now print their own build ID under `-V=full` (like devel toolchains) and cmd/go uses its content ID as the tool ID, so a rebuilt. The old rule "run `go clean -cache` after every make.bash" is obsolete. CI asserts the discriminator on every build platform.
- **An unset GOMEMLIMIT takes the cgroup's memory limit.** `readGOMEMLIMIT` reads `memory.max` (cgroup v2) or `memory.limit_in_bytes` (v1) of the process's own cgroup at `gcinit` and uses. An explicit `GOMEMLIMIT`, `off` included, still wins, and a host with no cgroups is unaffected. This holds for cosmo too: the APE asks `__hostos` first and only reads `/proc` on a Linux host. `internal/runtime/cgroup` builds for cosmo now, over `sys_cosmo.go`'s syscall shims.
- **An arm64 APE on macOS needs AT_HWCAP. It takes two fixes.** A reader without one reads the `ID_AA64ISAR*` registers - an `MRS` macOS answers. The APE loader does pass a pair, but it sets `hwcap_CPUID`, claiming the kernel emulates those registers.`fixAuxv` clears that bit in `osinit` (and. Never set `hwcap_CPUID`: it means "the kernel emulates those registers". Depth: DEBUGGING.md "AT_HWCAP" and "Working uname" (2026-09-03/04).
- **`/proc/self/auxv` is served by the APE off a Linux host.** A library written for Linux reads the auxiliary vector out of that file rather. `syscall.Openat` answers the path from `runtime.getAuxv`, handing back the read end of a pipe holding the pairs plus the AT_NULL terminator: before the real. So AT_HWCAP now reaches x/sys/cpu too, which is what stops the arm64 MRS fallback and its SIGILL. Depth: DEBUGGING.md "AT_HWCAP" (2026-09-04).
- **The pclntab format has diverged from upstream** (size pass 3b, 2026-07-19). Compact layout under magic `abi.CosmoPCLnTabMagic` (0xffffffc1): repacked 40-B `_func` records with presence-bitmap pcdata/funcdata arrays, prefix-split funcnametab, dir-split filetab, packed pctab pairs, 13-B InlTree records. Consequence: upstream debug/gosym-based tools cannot parse fork binaries. The fork's own debug/gosym, objdump, nm, and addr2line are updated. DWARF sidecars are unaffected, so gdb/delve work. Writer and readers must move in lockstep: `cmd/link/internal/ld/pcln.go` + `cmd/internal/obj/pcln.go` <-> `runtime/symtab.go`/`symtabinl.go` <-> `debug/gosym`.

## Local Verify Loop

```bash
cd src && ./make.bash                          # build toolchain (needs Go 1.24+ bootstrap)
export PATH="$PWD/../bin:$PATH"
# after linker or go-command changes:
GOOS=linux GOARCH=amd64 go install cmd/link cmd/go   # refresh HOST tools (see gotcha above)
GOOS=cosmo go build -o /tmp/fizzbuzz.com ./testdata/fizzbuzz/fizzbuzz.go   # emits fat APE
cd testdata/ape/apetest && FIZZBUZZ_BIN=/tmp/fizzbuzz.com go test -count=1 ./...   # upstream go
```

## Uprevving to a new upstream Go release

```bash
git fetch --no-tags https://github.com/golang/go.git refs/tags/goX.Y.Z:refs/tags/goX.Y.Z
git merge goX.Y.Z -m "all: merge goX.Y.Z into cosmo"
printf 'goX.Y.Zcosmo\n' > VERSION      # the one habitual conflict; drop upstream's "time" line
cd src && ./make.bash
export PATH="$PWD/../bin:$PATH"
GOOS=cosmo GOARCH=amd64 go build std && GOOS=cosmo GOARCH=arm64 go build std
GOOS=linux GOARCH=amd64 go test -short go/build cmd/internal/moddeps
```

**A patch bump and a minor bump are different jobs.** go1.26.4 and go1.26.5 each produced exactly one conflict (VERSION). go1.27.0 produced 73, because a. The single most useful triage tool is `git diff --name-only <previous tag> HEAD -- <file>`. An empty result means the fork never touched that file. The conflict is release-branch-versus-master noise and upstream's side is correct outright. Only the remainder needs judgement. Depth, including the go1.27.0 resolutions worth knowing about: docs/UPREV-GO1.27.md.

The work that is NOT in the conflict list is the class of break that produces **no** conflict: upstream re-partitions a platform file and. 46 upstream files carry a fork edit that is nothing but adding `cosmo` to a tag, and they cluster in the. `go build std` for both arches is what catches it. Do not skip it because the merge looked clean. When a symbol goes undefined. The fix is a new `*_cosmo.go` file, never a widened upstream tag.

A minor bump also moves internal APIs the fork's own code calls, and those break the BUILD rather than a tag. Build std for every port the fork supports, not just cosmo: `js/wasm`, `wasip1/wasm`, and both under `GOWASM=threads`. Regenerate what upstream generates — `go run -C=_gen .` in `cmd/compile/internal/ssa` — rather than hand-merging opGen.go.

Then sweep the version string (`grep -rn goX.Y '<old>cosmo'` across CLAUDE.md, README.md, docs/INSTALL.md, cosmo-ci.yml), and record the merge in DEBUGGING.md.

## CI

Per-step rationale trimmed from `cosmo-ci.yml`'s comments (1-line cap): docs/CI.md.

The GitHub Actions workflow (`.github/workflows/cosmo-ci.yml`) builds the toolchain on Linux, macOS, and Windows and tests that APE binaries built on any platform run correctly. The single `test` job is a 3-OS matrix (ubuntu/macos/windows). Every leg runs the full apetest suite against all 3 origin binaries. The windows-latest leg additionally runs two windows-only steps before the shared apetest steps: a never-failing AF_UNIX capability diagnostic (attributes any unixsock failure to runner. `fizzbuzz.com 10 5` prints `fizzbuzz\n`, exit 0). Its apetest steps - fizzbuzz battery AND runtimeprobe execution, via direct CreateProcess - keep the longer per-step timeouts the old dedicated windows job used, carried as.

CI builds one fat APE per platform. No GOARCH pin. The output contains cosmo amd64 and cosmo arm64 payloads, stripped by default, with apetest's `TestFatSidecarsExist` asserting the `.dbg`/`.aarch64.elf` sidecars exist on every build. The artifact ships the bare binaries, so apetest's TestDebugSidecars skips on the test runners). Structural format tests run everywhere. The full execution suite (fizzbuzz + runtimeprobe) runs on all three test runners.

**Every build leg runs `src/run.bash`** (`run.bat` on windows), which execs `go tool dist test -rebuild`. That is the distribution's own all-tests entry point and the only test gate here: the stdlib and `cmd` packages, the `test/` corpus through `cmd/internal/testdir`. A plain `go test` reaches none of the last two. It replaced a set of steps that each named the tests it wanted, which made green a statement about the name lists rather than. Never reintroduce a `-run` list here: a test nobody names never runs. Upstream runs the same suite per builder. A port is a host. This fork runs it on each of the three. `dist test` reads `GO_BUILDER_NAME`. A nameless builder gets the short set. Known red: cmd/go's `list_symlink_issue35941`, over the whole-repo vendor submodules.

The ubuntu leg keeps `dats/cosmo-tests.dats`, which runs the GOOS=cosmo package tests through the misc/cosmo wrappers: internal/runtime/syscall/cosmo (darwin sendmsg/recvmsg cmsg repack, signal translation tables, epoll layout), the runtime's Apple itimerval ABI pins. Those name lists live in the suite, where an engineer can run them, rather than in a workflow step. Every build leg additionally asserts, right after make.bash, a content-derived `buildID=` in `compile -V=full`. That is the cross-build cache-poisoning guard — see the tool-build-ID bullet in Fork Gotchas.

The ubuntu build leg also carries the uprev guardrail `GOOS=cosmo go build std` for amd64 and arm64 (2026-07-26). The execution suite compiles only what fizzbuzz and runtimeprobe import — 84 of the 358 std packages under cosmo — so an upstream re-partition of a. Run it locally before proposing an uprev — it is what turns a clean merge into a verified one.

Two test programs ship in each build's artifact: `fizzbuzz.com` (basic execution) and `runtimeprobe.com` (testdata/runtimeprobe - a multi-file module, built via its directory: file I/O, directory listing. Its `nanosleep` check asserts on the elapsed CLOCK, not on the error: a syscall that returns success without sleeping passes an error-only check, which. The apetest suite runs both against all three origin binaries via the FIZZBUZZ_BIN and RUNTIMEPROBE_BIN env vars. The macos-latest runner is what actually executes the darwin (Syslib) code paths.

A third job (`wasm`, ubuntu-only - wasm output is host-independent) regression-gates the fork's WebAssembly ports: it builds the toolchain, builds std for js/wasm and wasip1/wasm, runs the stdlib packages the.

Three more jobs (`publish-create`, `publish-upload`, `publish-finish`. They need build+test) publish an installable toolchain tarball to buildhost on every push, one leg per platform - see Toolchain Distribution below.

## Repository automation (pr-minder bot)

This repo, like the rest of the wow-look-at-my org, is watched by the org's **pr-minder** GitHub bot. Its observed behavior around branches, PRs, and labels — know this before pushing branches or interpreting PR state:

- **Auto-opened PRs.** Any lingering `claude/*` branch gets a **non-draft** PR auto-opened for it within about a minute of the push. Expect the PR to exist before you open one by hand. Edit the auto-opened PR (title/body) rather than opening a duplicate.
- **Label-triggered merges.** The bot merges a PR when the repository owner applies the `auto-pr-merge` label. Draft status is NOT protection: a green draft carrying the label is flipped ready-for-review and squash-merged within seconds. If the PR only goes green later (label already in place), the merge lands on the bot's next hourly reconcile pass instead of immediately. Head branches are deleted after merge.
- **Body regeneration.** The bot can regenerate/overwrite PR bodies during its update passes. If a PR body matters, keep a copy and re-apply it once after a rewrite — do not loop against the bot.
- **Base-branch updates.** The bot merges the base branch (master) into PR branches as siblings merge — ordinary forward merge commits, never force pushes. Pull before pushing to a branch the bot may have advanced.
- **Timeline attribution.** Ready-for-review, auto-merge, and merge events show the bot as the *actor* even when the repository owner initiated them by applying the label. Judge intent by the PR's `labeled` timeline events (who applied `auto-pr-merge`), not by the executor of the follow-on events. Symmetrically, the bot re-enforces state it was told to arm: reverting it (e.g. flipping the PR back to draft) is counter-flipped within seconds — a durable change needs the owner to.
- **Merge gating (`all-builds`).** Master only moves via PRs, and a PR only merges when its head SHA carries a green `all-builds` commit status — posted. See the DEBUGGING.md note in the PE-header work). Do not name any CI job `all-builds`: an org guard fails workflows that define one, because the status context is reserved for the aggregator.

## Shared build cache: the client is linked into `cmd/go`

The org's shared build cache is reached in process. `cmd/go` requires `github.com/wow-look-at-my/go-s3-server/cacheclient` and calls it from `cmd/go/internal/cache/shared.go`, which layers a network tier under the disk cache: disk stays authoritative. The shared tier is.

**`GOCACHEPROG` is deleted** — the variable, the protocol, and `cmd/go/internal/cacheprog`. `chooseCache` (`cache/default.go`) picks the shared tier over disk, or disk alone. Nothing forks a cache program, and a leftover `GOCACHEPROG` in the environment names nothing. The subprocess was the cost, not the feature: it answered with a PATH rather than bytes, so a program storing bodies in packs had.

`GO_BUILDCACHE_CONFIG` configures the tier (`cacheclient.ConfigFromEnv`). Unset. The build stays on disk. A run with `CI` set and no shared cache fails outright, because an unconfigured CI run decides whether every other CI run recompiles. `GOCACHEDEBUG` restores the client's per-request diagnostics during `shared.go`'s quiet window.

**No dependency source is copied into this tree.** `src/cmd` builds in vendor mode. The require needs its packages under `src/cmd/vendor/`. Those three paths are **git submodules**, not copied files, so this repo stores a commit pointer and the source keeps its own history and.

| vendor path | repository |
|---|---|
| `src/cmd/vendor/github.com/wow-look-at-my/go-s3-server` | the cache client |
| `src/cmd/vendor/github.com/wow-look-at-my/go-containers` | its `set` package |
| `src/cmd/vendor/github.com/pierrec/lz4/v4` | the cache's wire framing |

Consequences to know. **Clone with `--recurse-submodules`**, or `cmd/go` will not build. Every `actions/checkout` in `cosmo-ci.yml` passes `submodules: true` for the same reason. To move the client, check the submodule out at the commit you want and update the matching version in `src/cmd/go.mod`. **Never run `go mod vendor` here** —. Read `src/README.vendor` before adding any other `src/cmd` dependency: what looks like one import is a whole subtree of somebody else's repository.

That subtree carries packages the build never imports. One upstream test fails over them: cmd/go's `list_symlink_issue35941` runs `go list all` in GOPATH mode, which walks. A pruned vendor tree is what upstream's test assumes, and only `go mod vendor` or per-package repositories produce one. The check stays red while whole-repo.

## Toolchain Distribution

Every green push publishes installable toolchain tarballs to buildhost as project `gosmopolitan`, for **linux/amd64, darwin/arm64 and windows/amd64** — one release, each platform built on its. There is no cross-package shortcut).

```bash
curl -fL --compressed "https://dl.pazer.build/gosmopolitan?branch=master&os=linux&arch=amd64" | tar -xz   # or darwin/arm64, windows/amd64
export PATH="$PWD/go/bin:$PATH"
```

Every slot uploads a `.tar.gz`, windows included: a GOROOT is a tree, buildhost stores one blob per os/arch, and it serves `&fmt=zip` and the. So distpack drops upstream's windows-only `.zip`. The publish-only VERSION stamp (`go<base>.r<run_number>`) keeps each release's cmd/go tool-ID namespace disjoint. The committed VERSION stays `go1.27.0cosmo`. macOS Intel and linux/arm64 build from source. Depth — the three-job publish flow, the draft-on-failure guarantee, `GOTOOLCHAIN`, pinning with `?v=N`, and the rest of the consumer gotchas: docs/INSTALL.md.

## Updating vendored golang.org/x modules in src/ (Dependabot is disabled here)

`.github/dependabot.yml` disables Dependabot updates — version AND security — for `/src` and `/src/cmd`. Those are the Go distribution's own modules ("std" and "cmd"): Dependabot's stock `go get` dies with `go: std: "std" is not an importable package; see 'go help packages'`. It can neither run `go mod vendor` for std. Dependabot ALERTS stay enabled for visibility (repo Security tab). Resolve them manually:

1. Build this tree's toolchain: `cd src && ./make.bash`. Use `../bin/go` below. (No `go clean -cache` needed since 2026-07-20: tool IDs are content-derived, so a rebuilt toolchain never reuses stale cache entries — see Fork Gotchas.)
2. `cd src && GOOS=linux go get golang.org/x/net@vX.Y.Z && GOOS=linux go mod tidy && GOOS=linux go mod vendor` (pin GOOS to the host — the fork defaults to cosmo).
3. HTTP/2 needs no regeneration step. go1.27 deleted the generated bundle and moved the implementation into `src/net/http/internal/http2/`, an ordinary in-tree package. `golang.org/x/net/http2` is no longer synchronized with std, so a bump of that module does not reach net/http at all.
4. Check `git log -- src/vendor/` for fork-local vendored changes that re-vendoring may have wiped. Re-apply them.
5. Rebuild, then run the affected stdlib tests: `GOOS=linux go test net/http net crypto/tls cmd/internal/moddeps`.

`src/README.vendor` is the upstream authority on vendoring in std/cmd.

## Loop-aware inlining (all targets)

Upstream's inliner is frequency-blind without a profile, so a call in a hot loop gets the same 80-node budget as one on a cold. This fork adds the static frequency estimate every other production compiler has (`src/cmd/compile/internal/inline/loop.go`), acting on loop nesting at the CALL SITE: -1.1% median over. `-d=loopinline=0` restores upstream's decisions exactly and is the bisect switch for a suspected regression. The knobs, the measurements, and the two runtime annotations it needed: docs/LOOP-INLINING.md.

## WebAssembly (GOOS=js / GOOS=wasip1)

This fork diverges from upstream on both wasm ports: preemptible loops and synchronous stdio by default, real fetch/socket transports behind `GODEBUG=jsfetchnode=1` and `GOWASI=wasmedgesock`, DWARF. Every round, its measurements and its gates: docs/WASM.md. Remaining gaps: WASM_SHORTCOMINGS.md.

The exec wrappers live in `lib/wasm/` (not misc/wasm). Put it on PATH so `GOOS=js GOARCH=wasm go test` finds `go_js_wasm_exec`. The fork defaults to GOOS=cosmo, so always pin GOOS/GOARCH on wasm commands.

## Adding Cosmo Support to Standard Library Packages

When a stdlib package fails to build for `GOOS=cosmo`, follow these steps:

### 1. Identify Build Constraint Types

Go uses two types of build constraints:
- **`//go:build` directives** - Add `cosmo` to the constraint (e.g., `//go:build cosmo || linux || ...`)
- **Filename suffixes** - Files like `foo_linux.go` only build for Linux. Create `foo_cosmo.go` with equivalent functionality.

### 2. Check What Cosmo Already Has

Before creating new files, check existing cosmo implementations:
```bash
ls src/**/\*cosmo\*.go
grep -r "//go:build.*cosmo" src/
```

### 3. Runtime Platform Handling

Cosmopolitan binaries run on Linux, macOS, AND Windows at runtime (the NT surface is still growing wave by wave - see DEBUGGING.md) - so never bake in single-OS assumptions. When creating `_cosmo.go` files:
- Do not assume Linux-only features like `/proc` are available
- Cosmopolitan Libc translates Linux syscalls to native OS calls at runtime
- Test assumptions about what works on each platform

### 4. Syscall Wrappers

The `syscall` package uses `//sys` comments to generate wrappers. Check:
- `src/syscall/syscall_cosmo.go` - main syscall implementations
- `src/syscall/zsyscall_cosmo_amd64.go` - generated syscall stubs

If a function like `Listen` is defined as lowercase `listen` but callers expect uppercase `Listen`, add a wrapper:
```go
func Listen(s int, backlog int) (err error) {
    return listen(s, backlog)
}
```

### 5. Common Patterns

When adding cosmo to an existing `//go:build` constraint, use alphabetical order:
```go
//go:build cosmo || dragonfly || freebsd || linux  // cosmo first alphabetically
```

For filename-based constraints, create new files rather than modifying the build system.

## Debugging ARM64 Cosmo

**Keep `DEBUGGING.md` updated** when working on ARM64 cosmo support. Log:
- What you have tried (with debug exit codes used)
- What worked vs what failed
- Current hypothesis and next steps

This prevents going in circles and losing context across sessions.

## APE Binary Reference

**GOAL: Make our APE binaries work exactly like `~/Downloads/vim.com`**

The vim.com binary is a working APE that runs on macOS ARM64. When fixing APE generation:
1. Compare our output to vim.com's shell header structure
2. Match vim.com's macOS ARM64 handling (embedded APE loader, cc compilation)
3. Do not invent new approaches - copy what works in vim.com
