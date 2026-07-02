# ARM64 Cosmo Debugging Log

## Problem
Building Go programs for `GOOS=cosmo GOARCH=arm64` and running them on macOS ARM64 crashes with SIGSEGV (exit 139).

## Background
- APE binaries on macOS ARM64 use the APE loader (ape-m1.c)
- The loader provides a `Syslib` structure with Apple API function pointers
- Direct syscalls don't work on Apple Silicon - must use Syslib functions
- Host OS is passed in X3 (8 = XNU/macOS), Syslib pointer in X15

## Files Modified

### Assembly (`src/runtime/`)
- `rt0_cosmo_arm64.s` - Entry point, saves hostos/syslib from APE loader
- `sys_cosmo_arm64.s` - Syscall implementations with CHECK_DARWIN paths

### Go (`src/runtime/`)
- `os_cosmo_arm64.go` - ARM64-specific OS interface, Syslib structure
- `os_cosmo_arm64_m.go` - mOS struct with `waitsema uintptr` for semaphores
- `os_cosmo_arm64_sema.go` - semacreate/semasleep/semawakeup using dispatch_semaphore
- `os_cosmo_amd64_m.go` - mOS struct with `waitsema uint32` for futex (non-arm64)
- `os_cosmo.go` - Removed mOS struct (now in arch-specific files)
- `lock_futex.go` - Changed build constraint to exclude arm64
- `lock_sema.go` - Changed build constraint to include cosmo arm64

## Debug Exit Codes Used

| Code | Location | Result |
|------|----------|--------|
| 50 | rt0_cosmo_arm64.s cosmo_init_ok | ✓ PASS |
| 60 | sys_cosmo_arm64.s mmap entry | ✓ PASS |
| 61 | sys_cosmo_arm64.s sched_getaffinity entry | ✓ PASS |
| 65 | sys_cosmo_arm64.s sched_getaffinity return | ✓ PASS |
| 66 | sys_cosmo_arm64.s nanotime1 entry | ✓ PASS |
| 67 | sys_cosmo_arm64.s after clock_gettime | ✓ PASS |
| 68 | sys_cosmo_arm64.s nanotime1 return | NOT TESTED |
| 70 | sys_cosmo_arm64.s dispatch_semaphore_create | ✗ NOT REACHED |

## Fixes Applied

### 1. mmap Darwin Path (FIXED)
**Problem**: mmap was using Linux flags and FP-relative addressing broke after stack manipulation.
**Fix**:
- Load all arguments BEFORE stack alignment
- Translate MAP_ANONYMOUS (0x20) to MAP_ANON (0x1000)
- Save args to aligned stack, reload before call

### 2. madvise Darwin Path (FIXED)
**Problem**: No CHECK_DARWIN, was making direct SVC syscall on macOS.
**Fix**: Added Darwin path returning 0 (success).

### 3. mincore Darwin Path (FIXED)
**Problem**: No CHECK_DARWIN, was making direct SVC syscall on macOS.
**Fix**: Added Darwin path returning -1.

### 4. Futex ENOSYS (PARTIALLY FIXED)
**Problem**: cosmo used lock_futex.go but futex doesn't exist on macOS (returns ENOSYS).
**Fix**:
- Switched cosmo arm64 to use lock_sema.go instead
- Implemented semaphore functions using dispatch_semaphore from Syslib
- Split mOS struct into arch-specific files (waitsema type differs)

**Status**: Implementation complete but crash still occurs before semaphore code is reached.

## Current State

The crash (SIGSEGV, exit 139) happens:
- AFTER: clock_gettime in nanotime1 (exit 67 works)
- BEFORE: dispatch_semaphore_create (exit 70 not reached)

This means the crash is in `schedinit` somewhere between:
- `ticks.init()` (calls nanotime, works)
- First lock contention (would call semacreate)

Likely crashing in one of:
- `moduledataverify()`
- `stackinit()`
- `randinit()`
- `mallocinit()`

## Next Steps

1. Add debug exit at `open` or `read` syscall (called by randinit for /dev/urandom)
2. Add debug in mallocinit path (second mmap call?)
3. Check if stack/frame handling is correct for Go function calls
4. Verify TLS setup is correct for cosmo (currently uses TLS_linux, not TLS_darwin)

## TLS Concern

The tls_arm64.h defines:
- `GOOS_cosmo` → `TLS_linux` → uses `TPIDR_EL0` (0xd53bd040)
- `GOOS_darwin` → `TLS_darwin` → uses `TPIDRRO_EL0` (0xd53bd060)

On macOS ARM64, the TLS base is in TPIDRRO_EL0, not TPIDR_EL0. This might cause issues when Go code tries to access TLS. However, for non-cgo builds, save_g/load_g are no-ops so this might not matter.

## Test Binary

```bash
cd test_build
GOOS=cosmo GOARCH=arm64 /path/to/gosmopolitan/bin/go build -o minimal_arm64.com minimal.go
./minimal_arm64.com
echo "Exit: $?"
```

minimal.go:
```go
package main

func main() {
    println("Hello from cosmo!")
}
```

## Current Status (2025-01-18)

### WORKING
- Basic ARM64 APE binaries run on macOS ARM64
- `println()` (built-in) works correctly
- Thread creation via pthread_create works
- GC initialization works (bgsweep, bgscavenge)

### NOT WORKING
- `fmt.Println()` and `os.Stdout.Write()` crash with SIGSYS
- Any syscall via the `syscall` package crashes on darwin ARM64

### Root Cause of Remaining Issue
The `internal/runtime/syscall/cosmo/asm_cosmo_arm64.s` uses raw `SVC` for all syscalls. This works on Linux but causes SIGSYS on darwin because macOS ARM64 doesn't allow raw syscalls.

The Go runtime works because `runtime/sys_cosmo_arm64.s` uses `CHECK_DARWIN` macros to route to syslib functions. But the `syscall` package uses the generic `cosmo.Syscall6` which doesn't know about syslib.

### Fix Required
Need to modify `internal/runtime/syscall/cosmo/asm_cosmo_arm64.s` to:
1. Check if running on darwin (`runtime·hostos == 8`)
2. Route to syslib functions for common syscalls (write, read, open, close, etc.)
3. This requires a syscall number → syslib offset mapping table

### Recent Fixes Applied
1. **clone_darwin**: Fixed FP-relative access after RSP modification - load mp before SUB $48, RSP
2. **mstart_stub_cosmo**: Simplified to match darwin's mstart_stub exactly
3. **mStackIsSystemAllocated**: Returns true for cosmo ARM64 (pthread provides stack)
4. **getpid**: Added CHECK_DARWIN path using syslib offset 112
5. **rlimit.go**: Skip rlimit adjustment on cosmo (workaround for syscall issue)


## 2026-06-10: Fat APE + ARM64 Linux boot path

`GOOS=cosmo go build` now emits a fat amd64+arm64 APE (cmd/go builds the
sibling GOARCH and merges via `go tool link -apefat`). Three runtime fixes
made the arm64 image boot as a plain ELF on Linux (verified under
qemu-aarch64; previously SIGTRAP/SIGSEGV/sema fatal in sequence):

1. `rt0_cosmo_arm64.s` treated any boot without the XNU handoff (X3=8 +
   Syslib magic in X15) as fatal (debug BRK). Now: invalid handoff =>
   hostos=0 (Linux), syslib=nil, raw SVC syscalls.
2. `mStackIsSystemAllocated()` claimed system stacks for all of
   cosmo/arm64; on Linux clone() then crashed storing child setup at
   stack -8. Now runtime-dispatched: pthread stacks on macOS only.
3. `semasleep`/`semawakeup` knew only dispatch_semaphore ("semasleep
   without semaphore" on Linux). Now runtime-dispatched: futex-backed
   counting semaphore on Linux (mOS.waitsemacount).

Loader-side findings (also fixed): the boot-header printf encoder used the
shell `'\''` idiom for quote bytes, which ape-m1.c's decoder cannot parse
(it stops at the first raw quote - the arm64 header had 0x27 in e_phoff);
and `getApeLoaderSource()` searched the local filesystem instead of using
the in-repo `ape-m1.c.gz`, so CI binaries shipped with no loader at all.
Both boot headers must decode from the first 8192 bytes (the loader's scan
window); the linker asserts this.

Next: macOS ARM64 native execution via the compiled loader is exercised by
CI (macos-latest, exec tests unskipped). If it fails, the syslib-routed
syscall paths in sys_cosmo_arm64.s are the place to look - the Linux SVC
side is now known-good under qemu.


## 2026-07-02: Boot-header printf encoder: '%' must be octal-escaped

The printf blob encoder let 0x25 ('%') through as a literal. POSIX printf
treats a bare '%' in its format string as a conversion directive, so
whenever a variable boot-header byte (e_entry bytes 24-31, e_phoff bytes
32-39 - payload offsets like 0x25xxxx) happened to be 0x25, Linux
self-assimilation truncated/shifted the header write and permanently
corrupted the binary. '%' is now emitted as \045 (matching apelink.c),
alongside the existing quote/backslash octal escaping. Regression tests in
cmd/link/internal/ld/ape_test.go round-trip every byte value through both
a decoder mirroring printf/ape-m1.c semantics and a real sh printf.


## 2026-07-02: Wave 3 - linux/shared runtime + stdlib test enablement

Four bug fixes plus test infrastructure:

1. **EpollEvent ABI (linux/arm64, P0)**: the cosmo port had ONE packed
   12-byte EpollEvent for both arches, but the kernel packs epoll_event
   only on x86-64; on arm64 it is 16 bytes with data at offset 8. The
   kernel wrote 16-byte strides into netpoll's [128]EpollEvent (sized
   12) - 512-byte stack overrun, and netpoll never saw its eventfd
   wakeup data. Symptom: timers/sockets hang on arm64 Linux. Fixed
   per-arch like upstream (internal/runtime/syscall/cosmo defs +
   syscall ztypes_cosmo_arm64.go). Verified A/B under qemu-aarch64: old
   layout hangs a timers+TCP-echo program, fixed layout passes. Layout
   regression test: defs_cosmo_test.go.
2. **os.Executable**: was "not implemented for cosmo". Now
   executable_cosmo.go: /proc/self/exe first, Args[0] resolution
   (aix/openbsd-style) as the no-procfs fallback for macOS hosts.
   Unblocked the entire os/exec suite (54 fails -> 0), whose helpers
   spawn via os.Executable.
3. **StartProcess nil Files entry -> EBADF**: exec_cosmo.go shuffled
   attr.Files in place, treating a nil *os.File (fd ^uintptr(0)) as a
   real descriptor, and never initialized nextfd. Adopted upstream
   exec_linux.go's local []int copy; -1 now means "closed in child".
4. **Test scaffolding**: cosmo added to nbpipe_pipe2.go +
   export_pipe2_test.go tags; syscall gained Getpriority/Setpriority,
   PRIO_*, TCIFLUSH/TCIOFLUSH/TCOFLUSH, TIOCGPGRP/TIOCSPGRP (+
   export_cosmo_test.go Tcgetpgrp/Tcsetpgrp), IPV6_UNICAST_HOPS,
   AF_LOCAL. syscall and net test packages now compile and pass.

**Exec wrappers**: misc/cosmo/go_cosmo_{amd64,arm64}_exec - cmd/go picks
them up from $PATH (FindExecCmd), so plain `GOOS=cosmo go test` works on
a unix host: assimilated ELF/Mach-O exec directly, pristine APE via
/bin/sh.

Suite results (cosmo/amd64): os/exec 54->0, os 11->1 (only
TestExecutableDeleted, which passes once an APE binfmt_misc handler
`:APE:M::MZqFpD::/bin/sh:` exists - purely a host limitation),
os/signal 4->0, sync 1->0, syscall/net build-fail->pass, runtime
build-fail->8 (3 gdb interop, TestUnsafePoint "no symbol section" in
objdump, TestFakeTime windows-payload faketime compile, 2
TestGoroutineLeakProfile assimilation races - concurrent FIRST execs of
one pristine APE corrupt the boot-script parse mid-rewrite; retries
pass). Zero runtime crashes.

Known follow-ups: sys_cosmo_arm64.s asmdecl (mstart_stub_cosmo, settls
missing Go declarations - fails explicit `go vet runtime` on arm64);
APE symtab section for objdump; faketime skip for cosmo; boot-script
assimilation could flock/copy+rename to close the concurrent-first-exec
race.
