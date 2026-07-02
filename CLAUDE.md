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

macOS ARM64 status (2026-07-02): file I/O (create/read/write/stat/rename/
remove), getpid/getppid, NumCPU, the monotonic clock, os.Executable,
argv/env and Getwd/Chdir all work (CI-verified by the runtime probe). Still
missing on macOS hosts: timers/time.Sleep, sockets and os/exec (netpoll is
epoll-only; a darwin pselect-based poller is the next wave), signals
(rt_sigaction is stubbed; signal-translation wave), and directory listing
(getdents64 has no Apple equivalent). See DEBUGGING.md for the full list.

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
execution) and `runtimeprobe.com` (testdata/runtimeprobe: file I/O, pid,
NumCPU, monotonic clock, os.Executable, argv/env, wd round-trip). The
apetest suite runs both against all three origin binaries via the
FIZZBUZZ_BIN and RUNTIMEPROBE_BIN env vars; the macos-latest runner is
what actually executes the darwin (Syslib) code paths.

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
