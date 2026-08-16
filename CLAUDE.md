# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Don't Ask Stupid Questions

When there's a specification, **follow the specification**. Never ask "should I follow the spec or do something different?" - the answer is always follow the spec. That's what specs are for. If the implementation doesn't match the spec, fix the implementation.

## Project Overview

This is a fork of the Go programming language toolchain that adds support for **Cosmopolitan Libc** (`GOOS=cosmo`). Cosmopolitan enables building "Actually Portable Executables" (APE) - single binaries that run natively on Linux, macOS, and Windows without modification.

## No Rosetta Dependency

**APE binaries run natively on all platforms without emulation.** This is not a goal or theory - it's proven, working technology. Real APE executables (like `vim.com` from Cosmopolitan) already run natively on x86_64 Linux, x86_64 macOS, ARM64 macOS, and Windows today without Rosetta. This fork's own output executes on Linux, macOS, and (since the cosmo-native NT bring-up, wave 1) Windows - see Building Cosmopolitan Binaries below for the per-platform status.

- APE binaries contain native code for multiple architectures (AMD64 + ARM64)
- On ARM64 macOS, APE runs native ARM64 code - NOT x86_64 via Rosetta
- The cosmocc toolchain doesn't need Rosetta, neither do we
- If something "works via Rosetta", that's not actually working - it's a bug

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

To run tests under GOOS=cosmo on a Linux/macOS host, `export PATH="$GOROOT/misc/cosmo:$PATH"` so cmd/go finds the `go_cosmo_*_exec` wrappers (see `misc/cosmo/README.md`); then plain `GOOS=cosmo go test <pkg>` works.

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
# through the PE header - there is no windows payload). Unset = every
# platform, byte-identical to before the flag existed. An unknown token, an
# empty list, or a platform whose payload is missing fails the build; a
# selection that needs one architecture skips the sibling build entirely and
# is still stripped and given its sidecar. NOT a size win by itself: the APE
# header is a fixed 64K, so only dropping an ARCHITECTURE changes the size
# (-47% for amd64-only), and linux/amd64+darwin/arm64+windows/amd64 needs
# both payloads and weighs exactly what the fat APE does. What a subset buys
# is an accurate claim and a host refused by name. `go env GOCOSMOPLATFORMS` reports
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

Fat-build coverage: `go build` (with or without `-o`; a plain
single-main-package build defaults its output name and fattens too) and
`go install` both produce fat APEs. `go test` / `go test -c` binaries stay
thin on purpose: they are host-run throwaway artifacts executed right here
(via the `misc/cosmo` wrappers), and fattening would triple every test
compile.

Parallel sibling build (2026-07-26): the sibling-architecture build runs
CONCURRENTLY with the primary one. The two share no ordering constraint -
different GOARCH, different build-cache keys, different output paths - and
overlapping them reclaims each build's serial tail (cosmo links twice per
arch): runtimeprobe cold on 4 cores goes 15.4s -> 12.5s, ~19%, with user
time unchanged, and the output is byte-identical to a sequential build,
sidecars included. Builds whose package graph already saturates the CPU
(`go build std`) gain nothing, so the win is concentrated in exactly the
single-binary builds people run interactively. `GOCOSMOFATSEQ=1` (or `on`)
forces the old sequential behavior, which halves a fat build's peak memory
because the two link phases can no longer overlap - reach for it on a
memory-constrained machine, in preference to `GOCOSMOFAT=0`, which gives up
fat binaries entirely. The sibling's output is buffered and replayed after
the primary build's, so concurrent diagnostics never interleave, and a
failed primary build exits before the sibling is reported (one copy of each
error, not two). Implementation: `cosmoSibling` in
`src/cmd/go/internal/work/cosmofat.go`; the child is killed and its scratch
directory removed via `base.AtExit`, since the primary build can now fail
while it is still running.

Strip-and-sidecar default (2026-07-18): the fat merge embeds only each
payload's loadable span - the file range its program headers reference,
exactly what cosmocc's apelink ships - and writes the pristine unstripped
per-arch linker ELFs next to the output as `<output>.dbg` (amd64) and
`<output>.aarch64.elf` (arm64), the names cosmo libc's FindDebugBinary
probes. Naming is exact output name plus suffix: bare `go build` of
package `web` gives `web`, `web.dbg`, `web.aarch64.elf`, like cosmocc's
`hello`/`hello.dbg`/`hello.aarch64.elf`. `GOCOSMOSTRIP=0` (or `off`,
parsed like GOCOSMOFAT) restores full embedded payloads with no sidecars;
an explicit `-s` or `-w` in `-ldflags` also suppresses sidecars and embeds
the user-stripped payloads as-is. Stripping does not affect runtime
tracebacks or runtime/pprof (Go symbolizes via gopclntab, which lives in a
loaded segment); the sidecars are for gdb/delve and offline tools - see
DEBUGGING.md "debug sidecars" (2026-07-18).

Debug tiers (2026-07-19 + round 2 2026-07-20, GOCOSMODEBUG): `slim`
swaps the sidecars for debug-only ELFs (in-linker objcopy
--only-keep-debug: alloc contents dropped to NOBITS, symtab + all DWARF
kept, not runnable, ~-68% - runtimeprobe sidecar pair 7,818,173 ->
2,418,888 B); `min` is slim's sidecar shape plus generation-time DWARF
trims cmd/go injects into every cosmo compile (-dwarflocationlists=false
-gendwarfinl=0; explicit user -gcflags override them): sidecars shrink
another ~-38% (rp pair 1,502,680 B) at the cost of debugger
argument/local values (garbage or <optimized out>; file:line
backtraces, runtime tracebacks, and pprof stay exact - "backtraces yes,
variables no"; per-tier gcflags also fork the build cache, so the first
build after switching tiers recompiles); `compact` appends line-level
debug info (symtab + DWARF info/abbrev/line/rnglists/addr/frame;
loclists dropped) past the APE's load span and points the payload +
boot ELF headers at it, so the assimilated `.com` is debugger-readable
with no sidecar (runtimeprobe 5,517,216 -> 7,539,064 B, +38%; args show
<optimized out> - variables are sidecar territory). Since round 2 all
cosmo `.debug_*` sections are zstd-compressed (ELFCOMPRESS_ZSTD,
in-linker klauspost encoder, -13..-16% of stored DWARF vs the old
zlib): readers need gdb >= 13 / binutils >= 2.40 / Go debug/elf >= 1.21
(delve reads it; verified live with gdb 15.1 + dlv 1.27). Non-cosmo
targets keep upstream zlib. Full numbers, gdb/delve recipes, and
gotchas: DEBUGGING.md "GOCOSMODEBUG" (2026-07-19) and "debug round 2"
(2026-07-20).

Shipping APEs: distribute release binaries zstd-compressed - the two arch
payloads make APE images highly redundant, so the wire cost collapses.
Measured on a stdlib-heavy webserver (net/http, crypto/tls, image/png,
time/tzdata): 17.3 MB unstripped fat, 12.3 MB stripped default, and
3.6 MB on the wire after `zstd -19 --long=27`. Distribution-side only,
by design: there is no runtime self-extraction mechanism. buildhost can
repackage uploaded artifacts on the fly via its `fmt=` query parameter.

The resulting `.com` file runs on Linux, macOS, and Windows. The cosmo
amd64 image boots on x86-64 Linux (staged copy); the cosmo arm64
image boots on ARM64 Linux (installed `ape` loader, else a staged copy)
and ARM64 macOS (compiled APE loader, no Rosetta); on Windows the SAME cosmo amd64 image boots
natively through the APE's PE header (vim.com-style, no embedded second
build - the windows/amd64 PE payload was removed 2026-07-18): the entry
stub sets the runtime's NT personality live (__hostos=2) and joins the
common boot, with kernel32 resolved at runtime from two loader-filled
IAT slots.

Per-platform runtime status - what works today on each host an APE boots on, what is still missing, and the
forensics behind each: docs/PLATFORM-STATUS.md. In short: Linux amd64/arm64 complete; Windows amd64 complete
through NT bring-up wave 3 (still missing: Windows/arm64, file/pipe dup(2), off-host networking coverage);
macOS arm64 complete including signals, SIGPROF profiling and SCM_RIGHTS fd passing; macOS Intel structurally
correct but its runtime bring-up is UNTESTED - do not claim it works.

**Variadic libc calls must pass their variadic arguments on the STACK.** arm64-apple diverges from AAPCS64
here, so a variadic callee handed its argument in a register reads uninitialized stack memory and usually
succeeds while doing something other than what was asked (this silently unset FD_CLOEXEC for a third of all
descriptors). Use `runtime.cosmoLibcCallVariadic1` / `darwin_call_v3` for fcntl, open/openat with a mode, and
ioctl; never `cosmoLibcCall6` or `darwin_call`. The runtimeprobe `cloexec` check gates it.

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

- **This toolchain defaults to `GOOS=cosmo`.** Any `go build`/`go install`/`go test`
  run with the fork's `bin/go` targets cosmo unless you pin GOOS. Rebuilding a host
  tool needs e.g. `GOOS=linux GOARCH=amd64 go install cmd/link`, and test harnesses
  (like `testdata/ape/apetest`) should be run with an upstream Go so the test binary
  itself is executable on the host.
- **An APE never writes to itself.** The kernel cannot exec the file as it
  stands, so the bootstrap script stages a copy under
  `${TMPDIR:-${HOME:-/tmp}}/.ape-run-1/<file identity>/`, writes the host's real
  header (ELF on Linux, Mach-O on macOS) into THAT, and execs it. The APE keeps
  its bytes and its checksum, runs from a read-only path, and stays fat. As
  root, staging also registers the magic with binfmt_misc and binds the copy
  over the original path in a private namespace. See `docs/APE-STAGING.md`.
- **Tool build IDs are content-derived (2026-07-20).** Upstream derives
  release-toolchain tool IDs from the tools' `-V=full` version line; the fork
  stamps the same release-style version (`go1.26.5cosmo`) into every build, so
  any two fork builds used to share tool IDs — and hence action IDs — letting a
  warm build cache (a local GOCACHE, or a consumer's shared GOCACHEPROG tier)
  serve stale, ABI-incompatible objects across fork builds (startup SIGSEGVs).
  Fork tools now print their own build ID under `-V=full` (like devel
  toolchains) and cmd/go uses its content ID as the tool ID, so a rebuilt
  toolchain automatically invalidates cached objects. The old rule "run
  `go clean -cache` after every make.bash" is obsolete; CI asserts the
  discriminator on every build platform.
- **The pclntab format has diverged from upstream** (size pass 3b, 2026-07-19).
  Compact layout under magic `abi.CosmoPCLnTabMagic` (0xffffffc1): repacked
  40-B `_func` records with presence-bitmap pcdata/funcdata arrays,
  prefix-split funcnametab, dir-split filetab, packed pctab pairs, 13-B
  InlTree records. Consequence: upstream debug/gosym-based tools cannot parse
  fork binaries; the fork's own debug/gosym, objdump, nm, and addr2line are
  updated. DWARF sidecars are unaffected, so gdb/delve work. Writer and
  readers must move in lockstep: `cmd/link/internal/ld/pcln.go` +
  `cmd/internal/obj/pcln.go` <-> `runtime/symtab.go`/`symtabinl.go` <->
  `debug/gosym`.

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

The merge itself is rarely the work — go1.26.4 and go1.26.5 each produced
exactly one conflict (VERSION). The work is the class of break that
produces **no** conflict: upstream re-partitions a platform file and cosmo
falls off the edge of the new `//go:build` tags. 46 upstream files carry a
fork edit that is nothing but adding `cosmo` to a tag, and they cluster in
the `_unix`/`_posix`/`_other`/`_stub`/`_nonlinux` families upstream churns
most. `go build std` for both arches is what catches it; do not skip it
because the merge looked clean. When a symbol goes undefined, the fix is a
new `*_cosmo.go` file, never a widened upstream tag.

Then sweep the version string (`grep -rn goX.Y '<old>cosmo'` across
CLAUDE.md, README.md, cosmo-ci.yml), and record the merge in DEBUGGING.md.

## CI

The GitHub Actions workflow (`.github/workflows/cosmo-ci.yml`) builds the toolchain on Linux, macOS, and Windows and tests that APE binaries built on any platform run correctly on all three. The single `test` job is a 3-OS matrix (ubuntu/macos/windows); every leg runs the full apetest suite against all 3 origin binaries. The windows-latest leg additionally runs two windows-only steps before the shared apetest steps: a never-failing AF_UNIX capability diagnostic (attributes any unixsock failure to runner vs port) and real fizzbuzz invocations of the ubuntu-origin and windows-origin fat APEs (byte-comparing stdout against the apetest contract - e.g. `fizzbuzz.com 10 5` prints `fizzbuzz\n`, exit 0); its apetest steps - fizzbuzz battery AND runtimeprobe execution, via direct CreateProcess - keep the longer per-step timeouts the old dedicated windows job used, carried as per-OS matrix values.

CI builds one fat APE per platform; no GOARCH pin. The output contains cosmo
amd64 and cosmo arm64 payloads, stripped by default, with the build step's
`ls` asserting the `.dbg`/`.aarch64.elf` sidecars exist on every build
platform (sidecars are not uploaded; the artifact ships the bare binaries,
so apetest's TestDebugSidecars skips on the test runners). Structural
format tests run everywhere; the full execution suite (fizzbuzz +
runtimeprobe) runs on all three test runners, and the ubuntu build leg
also runs the cmd/link APE-merge/debug-view and cmd/go
strip/GOCOSMODEBUG/tool-ID/fat-parallel unit tests plus, via the misc/cosmo
wrappers, the GOOS=cosmo internal/runtime/syscall/cosmo package
tests (darwin sendmsg/recvmsg cmsg repack, signal translation
tables, epoll layout) and the runtime-package cosmo tests (Apple
itimerval ABI pins + timeval translation behind the darwin SIGPROF
setitimer dispatch, signal translation tables). Every build leg additionally
asserts, right after make.bash, that `compile -V=full` reports a
content-derived `buildID=` (the cross-build cache-poisoning guard —
see the tool-build-ID bullet in Fork Gotchas).

The ubuntu build leg also carries the two uprev guardrails (2026-07-26):

- **`GOOS=cosmo go build std` for amd64 and arm64.** The execution suite
  compiles only what fizzbuzz and runtimeprobe import — 84 of the 358 std
  packages under cosmo — so an upstream re-partition of a platform file
  (the go1.26.4 `fchmodat_linux.go`/`fchmodat_other.go` split, statat_unix.go's
  new tag) drops cosmo off the new build tags with no conflict and no red
  test. crypto/x509, archive/tar and net/http carry exactly those tags.
  ~40s cold for both arches.
- **`go test go/build cmd/internal/moddeps`.** `TestDependencies` is the
  only mechanical check that the fork's new packages sit where the tree's
  layering allows, and `TestVendorPackages` the only one guarding what may
  be vendored; both had silently gone red (see DEBUGGING.md 2026-07-26).
  Only `-run TestReadGoInfo` was running before.

Run both locally before proposing an uprev — they are what turn a clean
merge into a verified one.

Two test programs ship in each build's artifact: `fizzbuzz.com` (basic
execution) and `runtimeprobe.com` (testdata/runtimeprobe - a multi-file
module, built via its directory: file I/O, directory listing, pid,
NumCPU, monotonic clock, timers, TCP/UDP/unix sockets, signals
(sigpanic recovery, os/signal, async preemption, wait-status decode),
os/exec, os.Executable, argv/env, wd round-trip). The apetest suite
runs both against all three origin binaries via the FIZZBUZZ_BIN and
RUNTIMEPROBE_BIN env vars; the macos-latest runner is what actually
executes the darwin (Syslib) code paths.

A third job (`wasm`, ubuntu-only - wasm output is host-independent)
regression-gates the fork's WebAssembly ports: it builds the toolchain,
builds std for js/wasm and wasip1/wasm, runs the stdlib packages the wasm
fixes touch under node 22 (js) and wazero (wasip1), runs the full
testdata/wasip1sock reference-host suite (GOWASI=wasmedgesock TCP and UDP
end to end), runs the testdata/jsfetchstream streaming-upload e2e under
node, and runs the wasmexport compiler regression tests via
cmd/internal/testdir for both wasm targets.

A fourth job (`publish`, ubuntu-only, needs build+test) publishes an
installable toolchain tarball to buildhost on every push - see Toolchain
Distribution below.

## Repository automation (pr-minder bot)

This repo, like the rest of the wow-look-at-my org, is watched by the org's
**pr-minder** GitHub bot. Its observed behavior around branches, PRs, and
labels — know this before pushing branches or interpreting PR state:

- **Auto-opened PRs.** Any lingering `claude/*` branch gets a **non-draft**
  PR auto-opened for it within about a minute of the push. Expect the PR to
  exist before you open one by hand; edit the auto-opened PR (title/body)
  rather than opening a duplicate.
- **Label-triggered merges.** The bot merges a PR when the repository owner
  applies the `auto-pr-merge` label. Draft status is NOT protection: a green
  draft carrying the label is flipped ready-for-review and squash-merged
  within seconds. If the PR only goes green later (label already in place),
  the merge lands on the bot's next hourly reconcile pass instead of
  immediately. Head branches are deleted after merge.
- **Body regeneration.** The bot can regenerate/overwrite PR bodies during
  its update passes. If a PR body matters, keep a copy and re-apply it once
  after a rewrite — don't loop against the bot.
- **Base-branch updates.** The bot merges the base branch (master) into PR
  branches as siblings merge — ordinary forward merge commits, never force
  pushes. Pull before pushing to a branch the bot may have advanced.
- **Timeline attribution.** Ready-for-review, auto-merge, and merge events
  show the bot as the *actor* even when the repository owner initiated them
  by applying the label. Judge intent by the PR's `labeled` timeline events
  (who applied `auto-pr-merge`), not by the executor of the follow-on
  events. Symmetrically, the bot re-enforces state it was told to arm:
  reverting it (e.g. flipping the PR back to draft) is counter-flipped
  within seconds — a durable change needs the owner to change the labels.
- **Merge gating (`all-builds`).** Master only moves via PRs, and a PR only
  merges when its head SHA carries a green `all-builds` commit status —
  posted by an org-side app that aggregates every build on the SHA
  externally (cosmo-ci.yml needs, and has, no aggregator job; see the
  DEBUGGING.md note in the PE-header work). Do not name any CI job
  `all-builds`: an org guard fails workflows that define one, because the
  status context is reserved for the aggregator.

## Toolchain Distribution

Every green push publishes installable toolchain tarballs to buildhost as project `gosmopolitan`, for **linux/amd64 and
darwin/arm64** — one release, each platform built on its own runner (distpack packages what a HOST build produced, so
`GOOS=darwin GOARCH=arm64 ./make.bash -distpack` fails; there is no cross-package shortcut).

```bash
curl -fL --compressed "https://dl.pazer.build/gosmopolitan?branch=master&os=linux&arch=amd64" | tar -xz   # or os=darwin&arch=arm64
export PATH="$PWD/go/bin:$PATH"
```

The publish-only VERSION stamp (`go<base>.r<run_number>`) keeps each release's cmd/go tool-ID namespace disjoint; the
committed VERSION stays `go1.26.5cosmo`. Windows, macOS Intel and linux/arm64 build from source. Depth — the three-job
publish flow, the draft-on-failure guarantee, `GOTOOLCHAIN`, pinning with `?v=N`, and the rest of the consumer gotchas:
docs/INSTALL.md.

## Updating vendored golang.org/x modules in src/ (Dependabot is disabled here)

`.github/dependabot.yml` disables Dependabot updates — version AND security —
for `/src` and `/src/cmd`. Those are the Go distribution's own modules ("std"
and "cmd"): Dependabot's stock `go get` dies with `go: std: "std" is not an
importable package; see 'go help packages'`, and it can neither run
`go mod vendor` for std nor regenerate `src/net/http/h2_bundle.go`. Dependabot
ALERTS stay enabled for visibility (repo Security tab); resolve them manually:

1. Build this tree's toolchain: `cd src && ./make.bash`; use `../bin/go` below.
   (No `go clean -cache` needed since 2026-07-20: tool IDs are content-derived,
   so a rebuilt toolchain never reuses stale cache entries — see Fork Gotchas.)
2. `cd src && GOOS=linux go get golang.org/x/net@vX.Y.Z && GOOS=linux go mod tidy
   && GOOS=linux go mod vendor` (pin GOOS to the host — the fork defaults to
   cosmo).
3. If `x/net/http2` changed, regenerate `src/net/http/h2_bundle.go` with
   `x/tools/cmd/bundle` (the exact command is `src/net/http/http.go`'s
   `//go:generate bundle` directive, also echoed in h2_bundle.go's header).
4. Check `git log -- src/vendor/` for fork-local vendored changes that
   re-vendoring may have wiped; re-apply them.
5. Rebuild, then run the affected stdlib tests:
   `GOOS=linux go test net/http net crypto/tls cmd/internal/moddeps`.

`src/README.vendor` is the upstream authority on vendoring in std/cmd.

## Loop-aware inlining (all targets)

Upstream's inliner is frequency-blind without a profile, so a call in a hot loop gets the same 80-node budget
as one on a cold error path. This fork adds the static frequency estimate every other production compiler has
(`src/cmd/compile/internal/inline/loop.go`), acting on loop nesting at the CALL SITE: -1.1% median over nine
whole-task workloads, +5% text size. `-d=loopinline=0` restores upstream's decisions exactly and is the bisect
switch for a suspected regression. The knobs, the measurements, and the two runtime annotations it needed:
docs/LOOP-INLINING.md.

## WebAssembly (GOOS=js / GOOS=wasip1)

This fork diverges from upstream on both wasm ports: preemptible loops and synchronous stdio by default,
real fetch/socket transports behind `GODEBUG=jsfetchnode=1` and `GOWASI=wasmedgesock`, DWARF v5 with real
variable locations, frame-aware GC with a host-donated mark step, a dispatcher-free control flow (rounds 7-8,
~1.4-2x faster), and `GOWASM=threads` up to a multi-P scheduler. Every round, its measurements and its gates:
docs/WASM.md. Remaining gaps: WASM_SHORTCOMINGS.md.

The exec wrappers live in `lib/wasm/` (not misc/wasm); put it on PATH so `GOOS=js GOARCH=wasm go test` finds
`go_js_wasm_exec`. The fork defaults to GOOS=cosmo, so always pin GOOS/GOARCH on wasm commands.

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
- Don't assume Linux-only features like `/proc` are available
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
- What you've tried (with debug exit codes used)
- What worked vs what failed
- Current hypothesis and next steps

This prevents going in circles and losing context across sessions.

## APE Binary Reference

**GOAL: Make our APE binaries work exactly like `~/Downloads/vim.com`**

The vim.com binary is a working APE that runs on macOS ARM64. When fixing APE generation:
1. Compare our output to vim.com's shell header structure
2. Match vim.com's macOS ARM64 handling (embedded APE loader, cc compilation)
3. Don't invent new approaches - copy what works in vim.com
