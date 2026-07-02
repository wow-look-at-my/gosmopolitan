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

## 2026-07-02: Wave 2 - macOS Intel Mach-O correctness (structural)

The dd-assimilation Mach-O header (cmd/link/internal/ld/ape.go,
makeMachoHeader) was provably dead on arrival:

1. **Single R+X segment**: the whole ELF image was mapped with one
   LC_SEGMENT_64, initprot=maxprot=R+X. The data segment was therefore
   unwritable, so rt0's first instruction (store to runtime.__hostos)
   would fault.
2. **No BSS**: vmsize == filesize, so p_memsz > p_filesz zero-fill
   (.bss/.noptrbss) was never allocated.
3. **hostos never set**: LC_UNIXTHREAD zeroed all GPRs, so rt0 read
   CL=0 = Linux and would issue raw Linux syscalls on XNU (SIGSYS).
4. **Thread state too long**: 22 quadwords written where
   x86_THREAD_STATE64 is exactly 21 (16 GPRs + rip + rflags + cs/fs/gs);
   real bytes (192) exceeded declared cmdsize (184).
5. **No __PAGEZERO**, and - found while verifying against the actual
   XNU source (bsd/kern/mach_loader.c) - **nothing mapped file offset
   0**: parse_machfile unconditionally rejects (LOAD_BADMACHO) any
   executable in which no R+X segment maps the start of the file
   (found_header_segment, pass 3).

Fixes (verified structurally; there is no Intel-mac runner):

- makeMachoHeader now derives one LC_SEGMENT_64 per PT_LOAD:
  fileoff = payload offset + p_offset, vmaddr = p_vaddr, initprot =
  maxprot = translated p_flags, vmsize = p_memsz page-rounded (BSS).
  The text segment is extended down to file offset 0 (covering the APE
  header, like Cosmopolitan's ape.S does) to satisfy
  found_header_segment; __PAGEZERO covers [0, text vmaddr), i.e. up to
  0xffff0000 rather than the 4GB default, which XNU accepts (its own
  requirement is only that the region exist; cosmo's ape.S uses 2MB).
- LC_UNIXTHREAD: exactly 21 register quadwords, cmdsize == bytes
  written == 184, rip = ELF e_entry, rcx = 8 so rt0 sees CL = XNU
  (XNU applies the full LC_UNIXTHREAD register file via
  thread_setstatus). rsp stays 0 = kernel-allocated stack.
- Guards (Exitf): page alignment of fileoff/vmaddr (XNU load_segment
  rejects unaligned), vm-range overlap after rounding (real Go layout
  abuts exactly: text 0xffff0000+0xc9000 -> rodata 0x1000b9000 ->
  data 0x1001a3000), entry inside an R+X segment (validentry), header
  overrunning the APE loader region at 0x8000, memsz < filesz.
- The header is padded to the dd block size (8); the script's dd count
  stays derived from the real length (now 504 bytes -> count=63).
- rt0_cosmo_amd64.s: MOVBLZX CL before storing __hostos - only the low
  byte is defined by the boot protocol, and the dispatchers CMPL the
  full 32-bit value against 8.

Verification: cmd/link unit tests build a synthetic 3-load ELF (RX/R/RW
with memsz>filesz), run the real writeAPEFile pipeline, simulate the dd
transform, parse with debug/macho, and assert every kernel invariant
above. apetest (117 tests, was 107) does the same against the real
fizzbuzz APE, cross-checking the segment table 1:1 against the embedded
ELF phdrs. Old apetest assumptions (LC_UNIXTHREAD at fixed offset
0x1068, single segment) were deleted as wrong under the new shape.

Remaining for macOS Intel (out of wave-2 scope): darwin-amd64 runtime
bring-up - clone/futex/sigaction are ENOSYS stubs, usleep has a DIVQ
self-division bug, gettimeofday clobbers DX - and real-hardware
verification. Until then macOS Intel is "structurally correct,
execution untested".

## 2026-07-02: Wave 4+5 - macOS ARM64 runtime correctness + runtime probe in CI

Fixes on the darwin (Syslib) paths, each verified by the macos-latest CI
runner actually executing the binaries:

- nanotime clockid: nanotime1_darwin passed Linux CLOCK_MONOTONIC (1) to
  Apple clock_gettime, which has no clockid 1 (Apple MONOTONIC is 6); the
  EINVAL was ignored and uninitialized stack came back as "time". Now
  remaps 1->6, checks the result, falls back to CLOCK_REALTIME, and
  zeroes the result slots first. The generic dispatcher's clock_gettime
  translates 1->6 too.
- Apple->Linux errno translation: Syslib functions return -errno with
  APPLE numbering (EAGAIN 35, ENOSYS 78, ETIMEDOUT 60...); Go compares
  Linux values. One shared byte table + leaf helper
  (runtime.cosmo_xlat_errno_r0) used at every darwin errno boundary:
  dispatcher, mmap, pipe, fork, and the Go emulation (via __error()).
- runtime getpid: the darwin path called Syslib offset 112 - which is
  dispatch_semaphore_create - leaking a semaphore per call and returning
  the object pointer as the pid. GETPID DECISION TRAIL: the Syslib has no
  getpid; evolving the embedded ape-m1.c (append field + bump
  SYSLIB_VERSION to 11) was REJECTED because the compiled loader cache
  key is only the loader version string (${TMPDIR:-$HOME}/.ape-1.10) and
  any existing Mach-O there is reused blindly, so a stale v10 loader
  (possibly compiled from an upstream cosmo binary's embedded source)
  would satisfy the cache forever and the new field could never be
  trusted. Instead the runtime resolves getpid (and friends) through the
  Syslib's dlsym(RTLD_DEFAULT, ...) at osinit - works with every v6+
  loader, cached or fresh. The function pointer is cached, not the value,
  so fork children see their own pid.
- rawSyscallNoError/rawVforkSyscall (syscall package) issued raw SVC ->
  SIGSYS killed os.Getpid/Umask/forkExec on macOS. Now: fork routes to
  Syslib fork in pure assembly (fork-child safe); the id family/umask
  route through the dispatcher's Go slow path (dlsym-backed).
- Darwin file syscall emulation (dispatcher Go slow path): mkdirat,
  unlinkat, renameat(2), faccessat, chdir, readlinkat, fstatat, fstat,
  getcwd, fcntl, getrandom - with AT_FDCWD (-100 -> -2), AT_* flag,
  fcntl O_* flag and Apple-struct-stat translation. os.newFile treats
  cosmo like the BSDs (stat fd, keep regular files out of the netpoller)
  because netpollinit still throws on macOS. os.Create/Stat/Rename/
  Remove/Getwd/Chdir work on macOS for the first time.
- openat argument translation: Linux O_CREAT (0x40) arrived as Apple
  O_SHLOCK (os.Create never created anything) and Linux AT_FDCWD (-100)
  as an invalid fd (EBADF); both darwin openat sites now translate flags
  (shared helper with full table) and dirfd.
- semasleep passed relative ns as an absolute dispatch_time_t (positive
  values are mach ticks since boot!), so every timed wait fired
  immediately (busy spin). Now uses dispatch_walltime(NULL, ns).
- GOMAXPROCS was pinned to 1 on macOS (sched_getaffinity ENOSYS). Now
  sysctlbyname("hw.ncpu") via Syslib v10.
- amd64-darwin honesty: usleep's DIVQ self-division (raw ns as tv_usec),
  gettimeofday's garbage third argument (post-Sierra XNU writes
  mach_absolute_time through it -> memory corruption), sigprocmask how
  values (Linux 0/1/2 vs Apple 1/2/3). Intel-mac remains non-working
  (clone/futex ENOSYS) - these just stop active lies.
- P2 batch: SYS_OPEN=56 alias deleted; Syslib version gate (>= v8, clear
  write(2) message + exit 127 instead of reading past a shorter struct);
  access/connect/socket/sbrk0 no longer execute roulette SVCs on darwin
  (XNU dispatches on x16, which Linux-convention stubs never set);
  runtime pipe2 emulates O_CLOEXEC/O_NONBLOCK with fcntl on macOS or
  fails ENOSYS rather than silently dropping flags; go vet runtime is
  clean on both arches (mstart_stub_cosmo decl added, dead settls
  deleted).

Two hard-won assembly lessons (both caught by CI executing on
macos-latest, invisible on Linux since the darwin branches never run
there):

1. JMP-to-symbol from a FRAMED asm function does not pop the frame.
   Syscall6 contains BLs, so the assembler gives it an auto LR/FP frame;
   a tail JMP into the Go slow path left RSP 16 bytes low - every FP
   offset in the target shifted, caller stack corrupted, SIGBUS in os
   init. Fix: real CALL with outgoing args in an 88-byte frame. JMP
   remains correct only in genuine leaves (rawSyscallNoError, thunks).
2. Everything downstream of syscall.Syscall is inside the _Gsyscall
   window (entersyscall has run) - growing the stack there is a fatal
   "stack split at bad time". The whole darwin emulation must be
   nosplit; the 792-byte nosplit budget is reclaimed by doing the errno
   fetch (__error + translate) in a 16-byte-frame asm trampoline. The
   linker's nosplit check now statically proves the path can't split.

Wave 5: testdata/runtimeprobe (file I/O, pid/ppid, NumCPU, monotonic
clock, os.Executable, argv/env, wd round-trip; no network/timers/exec/
signals) + apetest runtimeprobe_test.go (RUNTIMEPROBE_BIN, skips when
unset) + CI wiring (built next to fizzbuzz.com, same artifact,
RUNTIMEPROBE_BIN in all three per-origin test steps). apetest: 118
tests.

Still broken on macOS (known, out of wave scope):
- Timers/time.Sleep, sockets, os/exec pipes: netpoll is epoll-only and
  netpollinit throws -> darwin poller (pselect+pipe) is the next wave.
- Signals: rt_sigaction is a success-stub; sigprocmask via
  pthread_sigmask still passes Linux `how` values (silent no-op) and
  Linux 8-byte sigsets; no darwin sigtramp. Signal wave.
- os/exec: fork works, but pipe2/dup3/execve are not emulated; forkExec
  fails with a clean ENOSYS instead of SIGSYS.
- getdents64 (directory listing) intentionally ENOSYS - Apple has no
  Linux-layout getdirentries.

New follow-up discovered (pre-existing, linux/arm64 host): syscall
Stat_t in ztypes_cosmo_arm64.go uses the amd64 layout, but the arm64
kernel writes the arm64 layout (mode at 16, not 24). Empirically: Mode
comes back 0 and Nlink 0x1000081ED for a 0755 file under qemu-aarch64.
Size/Ino/Timespec offsets happen to coincide, so size-based code works;
file-TYPE checks are wrong on arm64 Linux hosts. Fix = per-arch Stat_t
(and matching update to the darwin emulation's mirror struct).
