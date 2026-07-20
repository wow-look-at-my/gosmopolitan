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
# today's default, byte-identical). slim: debug-only sidecars, ~-67%,
# same names, not runnable - gdb/delve consume them unchanged. compact:
# slim sidecars PLUS line-level debug info appended to the APE past the
# load span (never mapped; ~+40% APE size) - gdb gets file:line
# backtraces from the assimilated .com alone, no sidecar present.
# Invalid values fail the build; GOCOSMOSTRIP=0 or -ldflags -s/-w make
# GOCOSMODEBUG a no-op (no sidecars to shape). See DEBUGGING.md.
GOCOSMODEBUG=slim GOOS=cosmo go build -o program.com main.go
GOCOSMODEBUG=compact GOOS=cosmo go build -o program.com main.go

# Explicit -s/-w wins: user-stripped APE, no sidecars (nothing to put in them)
GOOS=cosmo go build -ldflags="-s -w" -o program.com main.go

# Opt out of the fat build (single-architecture APE for the current GOARCH;
# thin builds never strip and get no sidecars)
GOCOSMOFAT=0 GOOS=cosmo GOARCH=amd64 go build -o program.com main.go

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

Debug tiers (2026-07-19, GOCOSMODEBUG): `slim` swaps the sidecars for
debug-only ELFs (in-linker objcopy --only-keep-debug: alloc contents
dropped to NOBITS, symtab + all DWARF kept, not runnable, ~-67% -
runtimeprobe sidecar pair 7,492,270 -> 2,441,136 B) with the shipped APE
byte-identical to default; `compact` additionally appends line-level
debug info (symtab + DWARF info/abbrev/line/rnglists/addr/frame, still
zlib'd; loclists dropped) past the APE's load span and points the
payload + boot ELF headers at it, so the assimilated `.com` is
debugger-readable with no sidecar (runtimeprobe 5,119,168 -> 7,167,264 B,
+40%; args show <optimized out> - variables are sidecar territory). The
default is unchanged and byte-identical with the knob unset. Full
numbers, gdb/delve recipes, and gotchas: DEBUGGING.md "GOCOSMODEBUG"
(2026-07-19).

Shipping APEs: distribute release binaries zstd-compressed - the two arch
payloads make APE images highly redundant, so the wire cost collapses.
Measured on a stdlib-heavy webserver (net/http, crypto/tls, image/png,
time/tzdata): 17.3 MB unstripped fat, 12.3 MB stripped default, and
3.6 MB on the wire after `zstd -19 --long=27`. Distribution-side only,
by design: there is no runtime self-extraction mechanism. buildhost can
repackage uploaded artifacts on the fly via its `fmt=` query parameter.

The resulting `.com` file runs on Linux, macOS, and Windows. The cosmo
amd64 image boots on x86-64 Linux (self-assimilation); the cosmo arm64
image boots on ARM64 Linux (self-assimilation) and ARM64 macOS (compiled
APE loader, no Rosetta); on Windows the SAME cosmo amd64 image boots
natively through the APE's PE header (vim.com-style, no embedded second
build - the windows/amd64 PE payload was removed 2026-07-18): the entry
stub sets the runtime's NT personality live (__hostos=2) and joins the
common boot, with kernel32 resolved at runtime from two loader-filled
IAT slots.

Windows status (2026-07-18, NT bring-up wave 2 COMPLETE - CI-verified
by the full runtimeprobe gauntlet on windows-latest, against binaries
built on all three platforms): stdout/stderr (console CP_UTF8+VT),
os.Args via GetCommandLineW, environment, os.Exit, VirtualAlloc memory,
CreateThread Ms, WaitOnAddress futexes, KUSER clocks, NumCPU; every
user-level syscall routes through an NT emulation dispatcher (Linux
numbers/errnos/structs in, Win32 out - src/runtime/os_cosmo_nt_sys.go)
covering process identity, ProcessPrng entropy, the whole file I/O
family with an fd table and a documented Linux<->Win32 path translation
(/tmp -> GetTempPathW, /c/... <-> C:\..., /dev/null -> NUL), getdents64
emulation (os.ReadDir/WalkDir/RemoveAll), working-directory round-trip,
os.Executable, and timers; os/exec (pipe2 over CreatePipe - blocking,
non-pollable on purpose - a posix_spawn-style CreateProcessW path with
upstream-ported quoting and env block, and wait4 packing the Linux
wait-status protocol: exit = code<<8, NTSTATUS crashes and encoded
signal deaths 0xC0DE0000|sig decode as Linux termination signals);
sockets over classic synchronous winsock (non-overlapped WSASocketW,
FIONBIO, AF_INET6 10<->23 and curated sockopt translation - SO_REUSEADDR
is swallowed for AF_UNIX because msafd accepts it and afunix.sys then
refuses bind - WSAE->errno map, SIO_UDP_CONNRESET disabled on UDP) with
a WSAPoll readiness netpoller (netpoll_aix.go's level-triggered two-lock
design; the wake channel is a connected loopback TCP pair because real
NT may drop loopback UDP datagrams - a lost wake stalls the poller;
pipes stay non-pollable/blocking on purpose); AF_UNIX pathname stream
sockets over afunix.sys (sun_path through the path layer; abstract
names refused EINVAL; wine's ws2_32 lacks AF_UNIX entirely, so wine
runs show exactly one red there while windows-latest proves it); and
signals: VEH-based sigpanic (SIGSEGV recover works), self-signals
(kill/tkill with full delivery through sigtrampgo), os/signal Notify,
async preemption via SuspendThread/SetThreadContext injection
(preempt ~180ms on the CI runner, upstream preemptM semantics), signal
deaths encoded for the wait4 protocol, and console Ctrl-C/Break/Close
-> SIGINT/SIGTERM via an asm handler + relay M. Still missing on
Windows: sendmsg/recvmsg (fd passing), SIGPROF profiling,
Windows/arm64, real-conhost console-ctrl injection coverage (the
handler is live but headless CI cannot generate console events) - see
DEBUGGING.md's NT wave sections (chunks A-E) for the ladder, the
forensics, and the wave-3 backlog.

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

## CI

The GitHub Actions workflow (`.github/workflows/cosmo-ci.yml`) builds the toolchain on Linux, macOS, and Windows and tests that APE binaries built on any platform run correctly on all three. The unix test legs run the full apetest suite against all 3 origin binaries. Windows execution coverage is the dedicated `test-windows` job: a never-failing AF_UNIX capability diagnostic (attributes any unixsock failure to runner vs port), real fizzbuzz invocations of the ubuntu-origin and windows-origin fat APEs (byte-comparing stdout against the apetest contract - e.g. `fizzbuzz.com 10 5` prints `fizzbuzz\n`, exit 0), and then the full apetest suite - fizzbuzz battery AND runtimeprobe execution, via direct CreateProcess - against all three origin binaries (ubuntu, windows, macos).

CI builds one fat APE per platform; no GOARCH pin. The output contains cosmo
amd64 and cosmo arm64 payloads, stripped by default, with the build step's
`ls` asserting the `.dbg`/`.aarch64.elf` sidecars exist on every build
platform (sidecars are not uploaded; the artifact ships the bare binaries,
so apetest's TestDebugSidecars skips on the test runners). Structural
format tests run everywhere; the full execution suite (fizzbuzz +
runtimeprobe) runs on all three test runners, and the ubuntu build leg
also runs the cmd/link APE-merge/debug-view and cmd/go
strip/GOCOSMODEBUG unit tests.

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
  binaries. Variable location expressions remained placeholders until
  round 6 gave them a real DW_OP_WASM_location frame base.
- Round 5 (2026-07-17): frame-aware GC. The pacer gives mark phases real
  runway (trigger no later than ~halfway to the goal, background credit
  seeded at cycle start so the allocation that crosses the trigger
  doesn't assist-burst), idle mark drains are bounded to 2ms, and a new
  `go_gc_mark_step(budgetMs) -> bool` wasm export lets the host donate
  idle time between frames (bounded mark work, no-op outside a cycle,
  returns whether work remains; while the host donates, the in-frame
  fractional mark quota drops 25% -> 5%). These behaviors are
  deliberately platform-independent - ALL platforms trigger earlier
  (trigger pinned ~halfway to the goal, trading some throughput/GC
  frequency for bounded mark bursts; TestGcPacer models the new math)
  and bound idle drains, and the budgeted mark step core is portable
  with runtime tests that run everywhere; only the event-loop yield
  glue (throttle idle marking + 1ms re-arm instead of starving the
  event loop until mark completion) and the wasm export are js-only.
  `testdata/framebench` (10k allocs/frame under node): p99 frame time
  21.6ms -> 4.8ms, zero frames over 8ms.
- Round 6 (2026-07-19): wasm DWARF variable locations are real: every
  subprogram's DW_AT_frame_base is now a DW_OP_WASM_location expression
  computing the frame base from the SP global (SP + framesize + 8 - the
  CFA of this x86-model target with a caller-pushed 8-byte return
  address; SP only moves in the prologue/epilogue, so the constant is
  exact throughout the body), and every stack-homed parameter and local
  resolves through its DW_OP_fbreg offset to exactly the linear-memory
  address codegen uses. The payoff is `-N -l` builds, where named
  variables live on the stack; heap-escaped variables still carry no
  location, and register-promoted variables in optimized builds stay
  name-and-type-only. llvm-dwarfdump decodes the expression natively
  and --verify stays clean; non-wasm DWARF is byte-identical; a
  cmd/link/internal/wasm regression test locks the encoding for both
  ports. In the same round, `GOWASI=wasmedgesock` grew real UDP:
  ListenUDP/ListenPacket with ReadFrom/WriteTo, connected `Dial("udp")`
  with Read/Write preserving datagram boundaries, and the
  same deadline machinery as TCP. Receives import the newer-generation
  `sock_recv_from_v2` (the plain-named `sock_recv_from` is WasmEdge's V1
  everywhere and cannot report the source port), so opt-in binaries now
  need WasmEdge 0.12+ (or the reference host) to instantiate at all -
  the generation mix, the 128-byte family-tagged address buffer, and
  the network-byte-order recv_from port quirk (verified live against
  WasmEdge 0.17.1) are documented in `syscall/net_wasip1_wasmedge.go`.
  ReadMsgUDP/WriteMsgUDP are ENOSYS
  (no ancillary data in the extension); DNS and unix sockets stay
  fake. `testdata/wasip1sock` grew UDP host support plus
  udpecho/udpconnected guests, and the CI wasm job now runs the whole
  wasip1sock suite. Default (no GOWASI) builds are unchanged. And the
  js fetch transport streams request bodies: unknown-length bodies
  (outgoingLength < 0 - exactly the requests HTTP/1 sends chunked)
  upload through a ReadableStream with duplex "half" instead of being
  buffered whole, when a cached one-time probe shows the runtime's
  fetch supports upload streaming (Node.js 18+ and Chromium 105+ pass;
  everything else keeps the buffered path byte-for-byte). Pulls read
  64KiB chunks on a goroutine off the event loop, so backpressure
  reaches the reader, and the body is closed exactly once on every
  path (EOF, read error, cancel/abort, every RoundTrip exit).
  Known-length bodies stay buffered on purpose - fetch drops
  Content-Length for stream bodies, buffering keeps it on the wire -
  and streamed bodies cannot be replayed, so a redirect that must
  re-send the body fails with a network error per spec.
  `testdata/jsfetchstream` is the node e2e; the CI wasm job runs it.
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
- Threads runtime B2 (2026-07-17): real Go code runs on worker threads
  under GOWASM=threads. Futex layer over memory.atomic.wait32/notify
  (`runtime/sys_wasmthreads.s`); futex mutexes/notes for the runtime
  (`lock_jsthreads.go`; `notetsleepg(n,-1)` parks the g, not the M);
  `newosproc` hands new Ms through a futex mailbox to pool workers
  parked in the new `wasm_thread_run` export (raw-wasm wait loop, sets
  per-instance SP/g to the M's heap-allocated g0 and enters mstart).
  `wasm_exec_node.js` pre-spawns GOWASMTHREADSPOOL workers (default 4,
  0 disables; newosproc throws after 10s if none claims); workers get
  real pure-runtime imports (println/clock/random/exit work on worker
  Ms) but syscall/js stays main-thread-only. The event loop remains
  main-M-only (`event_js.go` shared; beforeIdle routes worker Ms to
  futex parks/timed sleeps). Main M may futex-wait (node-only; event
  loop stalls while it does - B3 makes that non-blocking). GOMAXPROCS
  is still clamped to 1 (single P handed between Ms); the demo/test
  hook `runtime.wasmThreadsRunOnNewM` (pull-linkname with
  `-ldflags=-checklinkname=0`) pins the caller's goroutine to its M to
  force the handoff; public LockOSThread is still a wasm no-op. Demo:
  `testdata/wasmthreads/threaddemo` (CI runs it 10x; three Ms on three
  threads, channels/mutex/shared heap/GC, exit 0), plus an in-tree
  runtime spawn test; threads regression set is `go test -short sync
  sync/atomic internal/runtime/atomic runtime` under GOWASM=threads.
  Non-threads builds keep identical behavior but are NO LONGER
  byte-identical across this phase (runtime source split shifts
  symbols/pclntab/DWARF). Still missing (B3): multi-P, real STW
  preemption, non-blocking main-thread park, syscall/js forwarding
  from worker Ms, browser workers.
- Threads B3 (2026-07-17) adds the multi-P scheduler bring-up: GOMAXPROCS is
  unclamped under GOWASM=threads (capped at GOWASMTHREADSPOOL+1; default still
  1), real 0xFE atomic bodies + publication fence, cooperative stop-the-world
  via cross-thread-armed loop backedge checks, a non-blocking main-thread park
  (main M releases its P and parks in the event loop; woken cross-thread via
  Atomics.waitAsync on a wake word, with a keep-alive while workers run), and
  syscall/js calls from worker Ms migrating their goroutine to the main
  thread (stdout/stderr writes work directly on workers). The B3
  "lost-wakeup" stalls / exit hang were root-caused to two bugs and fixed:
  a main-thread microtask livelock (a wasmMainWake bump issued on the main
  thread inside a resume re-triggers the armed Atomics.waitAsync watcher,
  and the microtask chain starves every macrotask incl. the worker-posted
  exit message; fixed by dropping main-thread-issued bumps) and
  migrate-queue starvation (the queue only the main M can pop had a single
  push-time wake, and a worker M idling in beforeIdle's timed sleep held
  the only P through the whole wait; fixed by re-nudging from pidleput and
  the parked-worker watchdog, and bailing out of the idle P-hold when the
  main M needs a P).

 Threads B4 (2026-07-19) is the hardening sweep: the rare GOMAXPROCS=4
  crash class was root-caused to a FALSE deadlock report - checkdead's
  "all Ms idle + runnable g" inference does not hold under threads
  because the parked main M executes Go code (self-serve/kicks) while
  still linked on sched.midle; checkdead now nudges the wake machinery
  and returns instead of throwing (real deadlock reporting via the
  host's exit-time probe is unchanged). The pool-headroom perf collapse
  is fixed: a main M parked in the event loop counts as the far-future
  -timer covering agent (backstop JS timeout + wakeNetPoller nudge), so
  loop gates disarm without pool headroom - pool sizing no longer
  affects parallel throughput for the common shapes. Idle-CPU
  double-counting that made /cpu/classes/user:cpu-seconds non-monotonic
  at >1P is fixed. Host events arriving while the main M has no P are
  queued by wasm_exec.js instead of silently overwriting _pendingEvent.
  Worker-side traps report their wasm stack (Go function names). New
  gates: testdata/wasmthreads/holblock (a blocked ASYNC event handler
  does not head-of-line-block later events; blocking a SYNCHRONOUS
  nested js.FuncOf callback stays documented-unsupported, as upstream)
  and testdata/wasmthreads/memgrow (worker-side memory.grow under
  concurrent main/worker heap+host traffic). Later phases: host-call
  forwarding, mark-worker knobs, browser hosts.

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
