# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Don't Ask Stupid Questions

When there's a specification, **follow the specification**. Never ask "should I follow the spec or do something different?" - the answer is always follow the spec. That's what specs are for. If the implementation doesn't match the spec, fix the implementation.

## Project Overview

This is a fork of the Go programming language toolchain that adds support for **Cosmopolitan Libc** (`GOOS=cosmo`). Cosmopolitan enables building "Actually Portable Executables" (APE) - single binaries that run natively on Linux, macOS, and Windows without modification.

## No Rosetta Dependency

**APE binaries run natively on all platforms without emulation.** This is not a goal or theory - it's proven, working technology. Real APE executables (like `vim.com` from Cosmopolitan) already run natively on x86_64 Linux, x86_64 macOS, ARM64 macOS, and Windows today without Rosetta.

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
# go build always builds both architectures and merges them
GOOS=cosmo go build -o program.com main.go

# go install produces the same fat APE in the install directory
GOOS=cosmo go install ./cmd/program

# Opt out of the fat build (single-architecture APE for the current GOARCH)
GOCOSMOFAT=0 GOOS=cosmo GOARCH=amd64 go build -o program.com main.go

# Merge two single-arch cosmo binaries plus a Windows PE into one fat APE by hand
go tool link -apefat amd64.com,arm64.com,windows.exe -o program.com
```

Fat-build coverage: `go build` (with or without `-o`; a plain
single-main-package build defaults its output name and fattens too) and
`go install` both produce fat APEs. `go test` / `go test -c` binaries stay
thin on purpose: they are host-run throwaway artifacts executed right here
(via the `misc/cosmo` wrappers), and fattening would triple every test
compile. The `faketime` build tag also forces a thin build, because its
windows payload cannot compile (`runtime/time_fake.go` is `!windows`).

The resulting `.com` file runs on Linux, macOS, and Windows. The cosmo amd64
image boots on x86-64 Linux (self-assimilation); the cosmo arm64 image boots
on ARM64 Linux (self-assimilation) and ARM64 macOS (compiled APE loader, no
Rosetta). Windows uses an embedded native windows/amd64 PE payload.

macOS ARM64 status (2026-07-02, wave 9): file I/O (create/read/write/stat/
rename/remove), directory listing (os.ReadDir/filepath.WalkDir/os.RemoveAll
via a getdents64 emulation over Apple's __getdirentries64),
getpid/getppid, NumCPU, the monotonic clock, timers (time.Sleep/Ticker/
After, context timeouts), TCP/UDP loopback sockets with deadlines,
unix-domain stream sockets (pathname addresses; the abstract namespace
is Linux-only and refused EINVAL), readv/writev (net.Buffers), os/exec
(fork, pipes, execve, wait4 with Linux-numbered wait statuses),
os.Executable, argv/env, Getwd/Chdir, and SIGNALS all work (CI-verified
by the runtime probe on macos-latest): SIGSEGV -> sigpanic/recover,
os/signal Notify delivery, async preemption (SIGURG - tight loops no
longer hang GC/STW), and kill/raise, with full Linux<->Apple
signal-number and sigset translation at every darwin boundary (tables
in src/runtime/sigxlat_cosmo.go). SIGPIPE additionally stays suppressed
per-socket via SO_NOSIGPIPE, matching Go's EPIPE-error semantics. As of
wave 9 the darwin netpoller is a kqueue port of upstream
netpoll_kqueue.go (kqueue/kevent via dlsym) and M parking is upstream
os_darwin.go's pthread_mutex+pthread_cond design - this pair replaced
the poll(2)+self-pipe poller and dispatch-semaphore parking after the
waves-6..9 nondeterministic macOS CI wedge was root-caused (by in-CI
counter forensics, DEBUGGING.md wave 9) to XNU sporadically never
returning from a nonblocking read(2) on the poller's wakeup pipe.
Still missing on macOS hosts: sendmsg/recvmsg (msghdr/cmsghdr layouts
differ; blocks fd-passing and ReadMsg*) and setitimer-based SIGPROF
profiling. See DEBUGGING.md for the full list.

macOS Intel status: the dd-assimilated Mach-O is structurally correct as of
2026-07-02 (per-PT_LOAD segments with real protections and BSS, __PAGEZERO,
host-OS handoff in rcx - verified against the XNU loader's checks by cmd/link
unit tests and apetest), but the darwin-amd64 runtime side (clone/futex/
sigaction and friends) is still incomplete, and there is no Intel-mac CI
runner, so end-to-end execution there is UNTESTED. Do not claim macOS Intel
"works" until the runtime bring-up lands and is verified on real hardware.

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
- **APE binaries self-assimilate.** Executing an APE rewrites its own header in
  place to the host's native format (ELF on Linux, Mach-O on macOS). Inspect or
  upload only pristine copies; run a throwaway copy (apetest's `copyBinary` does
  this automatically).

## Local Verify Loop

```bash
cd src && ./make.bash                          # build toolchain (needs Go 1.24+ bootstrap)
export PATH="$PWD/../bin:$PATH"
# after linker or go-command changes:
GOOS=linux GOARCH=amd64 go install cmd/link cmd/go   # refresh HOST tools (see gotcha above)
GOOS=cosmo go build -o /tmp/fizzbuzz.com ./testdata/fizzbuzz/fizzbuzz.go   # emits fat APE
cd testdata/ape/apetest && FIZZBUZZ_BIN=/tmp/fizzbuzz.com go test -count=1 ./...   # upstream go
```

## CI

The GitHub Actions workflow (`.github/workflows/cosmo-ci.yml`) builds the toolchain and tests that APE binaries built on any platform (Linux/macOS/Windows) run correctly on all other platforms.

CI builds one fat APE per platform; no GOARCH pin. The output contains cosmo
amd64, cosmo arm64, and native windows/amd64 payloads. Execution and structural
format tests run everywhere.

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
fixes touch under node 22 (js) and wazero (wasip1), and runs the
wasmexport compiler regression tests via cmd/internal/testdir for both
wasm targets.

A fourth job (`publish`, ubuntu-only, needs build+test) publishes an
installable toolchain tarball to buildhost on every push - see Toolchain
Distribution below.

## Toolchain Distribution

Every push whose build+test jobs are green publishes an installable
linux-amd64 toolchain tarball to buildhost (pazer.build) as project
`gosmopolitan`: the `publish` job in cosmo-ci.yml runs `make.bash -distpack`
(official packaging; output `pkg/distpack/go<VERSION>.linux-amd64.tar.gz`,
currently `go1.26.4cosmo.linux-amd64.tar.gz`, ~64 MiB) and uploads it via
GitHub Actions OIDC (audience `https://pazer.build`; direct PUT below
server-info's `max_direct_upload_bytes`, chunked upload session above it).
Consumers install the fork in seconds instead of a ~3 minute `make.bash`:

```bash
curl -fL --compressed "https://dl.pazer.build/gosmopolitan?branch=master&os=linux&arch=amd64" | tar -xz
export PATH="$PWD/go/bin:$PATH" GOTOOLCHAIN=local
go version   # go version go1.26.4cosmo linux/amd64
```

The tarball extracts to `go/` (official distribution layout; GOROOT is
derived from the binary location, no need to set it). Consumer gotchas:

- **Set `GOTOOLCHAIN=local`.** The shipped `go.env` keeps upstream's
  `GOTOOLCHAIN=auto`, so a consumer go.mod with a `go`/`toolchain` directive
  newer than this fork's version would silently download an official
  toolchain and lose cosmo.
- **Pin GOOS on host-side builds.** The fork defaults `GOOS=cosmo` (see Fork
  Gotchas); any host-run `go build`/`go install`/`go test` needs
  `GOOS=linux GOARCH=amd64`.
- **Pinning**: `?branch=master` is a rolling latest that moves on every push
  to master (each branch gets its own `?branch=<name>` latest). Pin an
  immutable release with `?v=N` in place of the `branch` param; buildhost
  auto-increments N per publish, the publish job logs it, and
  `https://pazer.build/api/v1/projects/gosmopolitan/releases/latest` resolves
  the current one.

## WebAssembly (GOOS=js / GOOS=wasip1)

This fork diverges from upstream on the wasm ports (upstream inherited them
untouched until 2026-07-04; see `WASM_SHORTCOMINGS.md` at the repo root for
the full catalog of fixes and remaining gaps):

- **Preemptible loops are default-on for GOARCH=wasm**: CPU-bound goroutines
  no longer starve timers/GC/other goroutines. Opt out with
  `GOEXPERIMENT=nopreemptibleloops`.
- `GOMAXPROCS` from the environment is clamped to 1 (no more `newosproc`
  throw at startup).
- The js argv/env budget is 60KB (61440 bytes, was 8KB), so big CI
  environments run.
- `GODEBUG=jsfetchnode=1` enables real HTTP via fetch under Node.js >= 18
  (default stays on the fake in-memory network).
- wasip1 honors `TZ` (with `time/tzdata` or a preopened zoneinfo dir).
- Round 2 (2026-07-05): atomic ops are intrinsified and int64 division is
  inlined (no more runtime calls), `syscall/js` adds `js.Await` and
  `js.CopyToGo`/`js.CopyToJS` (typed-array bulk copies), and the periodic
  2-minute forced GC now runs on both wasm ports.
- Round 3 (2026-07-05): stdout/stderr are synchronous under node (printing
  no longer hangs while another goroutine is CPU-busy), fully-idle js
  programs are woken for the periodic GC via a weak unref'd timeout,
  `GOWASM=tailcall` emits return_call (js-only; wazero rejects it, so it
  stays off by default), CPU profiling works at 100Hz on both ports
  (`pprof.StartCPUProfile`, `-test.cpuprofile` - sampled at loop
  backedges), and `go tool objdump`/`nm`/`addr2line` understand wasm
  binaries. `runtime/pprof` joined both test lists in the CI wasm job.
- Round 4 (2026-07-06): `GOWASI=wasmedgesock` (default off) gives wasip1
  real TCP sockets via the WasmEdge socket extension (Dial, Listen/Accept,
  deadlines, http.Get/http.Serve); `testdata/wasip1sock` holds the wazero
  reference host plus the end-to-end tests. Default builds are unchanged.
  In the same round, wasm binaries gained DWARF v5 debug info in
  `.debug_*` custom sections per the WebAssembly DWARF convention (code
  addresses are byte offsets from the start of the code section's
  contents, the lld/Chrome model): full DIE tree plus statement-level
  line tables, `llvm-dwarfdump --verify` clean on both ports. On by
  default (~+39% file size), stripped with `-ldflags=-w`. The name
  section now precedes producers, so llvm tools can read Go wasm
  binaries. Variable location expressions are still placeholders
  (faithful locations need DW_OP_WASM_location).
- Threads groundwork B0 (2026-07-17): `GOWASM=threads` (default off,
  experimental, GOOS=js only) is toolchain-only groundwork for the wasm
  threads proposal - real parallelism lands in later phases and the
  runtime stays single-threaded for now. With it, Go's atomic ops emit
  the proposal's 0xFE atomic instructions and the linker imports a
  shared linear memory (`gojs`.`mem`, shared limits flag 0x03, max
  2048 MiB) instead of declaring a module-local one; `wasm_exec.js`
  supplies the matching shared `WebAssembly.Memory` via
  `go.provideMemory(wasmBytes)` (called automatically by
  `wasm_exec_node.js`, no-op for ordinary modules). Node 18+ needs no
  flags; browsers will need COOP/COEP headers (cross-origin isolation)
  for SharedArrayBuffer. wasip1 builds reject the flag at link time
  (wazero/wasmtime lack the proposal). Without the flag, output is
  byte-identical to before.
- Threads worker pool B1 (2026-07-17): under GOWASM=threads the linker
  now emits PASSIVE data segments (+ DataCount section) - active ones
  would be re-applied on every instantiation, so a second instance would
  clobber live heap/runtime state in the shared memory - plus two
  synthetic exports: `_initmem` (memory.init + data.drop of all
  segments; called exactly once, by the main instance, from `Go.run` in
  wasm_exec.js - worker instances must never call it) and
  `wasm_probe_atomic_add(addr, delta)` (a runtime-state-free wasm
  i32.atomic.rmw.add workers can call before the scheduler is
  thread-aware). `wasm_exec_node.js` compiles threads modules once
  (kept on `go._module`); `lib/wasm/wasm_exec_pool_node.js`
  (`GoWorkerPool`) + `wasm_exec_worker_node.js`/`wasm_exec_worker.js`
  spawn N node worker_threads that instantiate the same module against
  the same shared memory with all gojs runtime imports stubbed - Go
  code does not run on worker instances yet (that is scheduler
  integration, phase B2). Demo: `testdata/wasmthreads/pooldemo` driven
  by `pool_demo.js` (run in CI). Ordinary modules and non-threads
  builds are unchanged (byte-identical).

The wasm exec wrappers live in `lib/wasm/` (not misc/wasm). Put it on PATH so
`GOOS=js GOARCH=wasm go test <pkg>` finds `go_js_wasm_exec` (Node.js 18+) and
`GOOS=wasip1 GOARCH=wasm go test <pkg>` finds `go_wasip1_wasm_exec`
(wasmtime by default; `GOWASIRUNTIME=wazero` also works). Remember the fork
defaults to GOOS=cosmo: always pin GOOS/GOARCH on wasm commands.

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

Cosmopolitan binaries run on Linux, macOS, AND Windows at runtime. When creating `_cosmo.go` files:
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
