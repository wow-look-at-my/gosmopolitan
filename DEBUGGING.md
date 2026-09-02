# ARM64 Cosmo Debugging Log

> Chronological log, oldest first. Early sections (through "Current Status
> (2025-01-18)") are historical: the crashes and missing syscalls they
> describe have since been fixed. For the current state, read the newest
> dated wave entry at the bottom and the status section in CLAUDE.md.

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
objdump, TestFakeTime windows-payload faketime compile [resolved
2026-07-18: fat APEs no longer embed a windows payload], 2
TestGoroutineLeakProfile assimilation races - concurrent FIRST execs of
one pristine APE corrupt the boot-script parse mid-rewrite; retries
pass). Zero runtime crashes.

Known follow-ups: sys_cosmo_arm64.s asmdecl (mstart_stub_cosmo, settls
missing Go declarations - fails explicit `go vet runtime` on arm64);
APE symtab section for objdump; faketime skip for cosmo [resolved
2026-07-18: no windows payload, faketime builds fat]; boot-script
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
[FIXED in wave 6 - see below.]

## 2026-07-02: Wave 6 - darwin netpoller, sockets, os/exec; arm64 Stat_t; go install fat

macOS hosts went from "any timer, socket or exec pipe kills the
process" (netpollinit threw: epoll-only) to timers, TCP/UDP and os/exec
all CI-verified on macos-latest. Each piece landed with the probe checks
that prove it, and every commit's CI was green on all six jobs.

**Netpoll design** (netpoll_cosmo_xnu.go): both pollers ship in one
binary - GOOS=cosmo cannot build-tag-split Linux and macOS - so every
netpoll entry point in netpoll_cosmo.go dispatches at run time on
__hostos; the Linux epoll path is byte-for-byte untouched. The darwin
side is a port of netpoll_aix.go: level-triggered poll(2) over a
mutex-protected pollfd array, nonblocking self-pipe for wakeups, one M
polling at a time (it holds xnuMtxset across the blocking poll; fd-set
mutators boot it out through the pipe first). poll itself is Apple
libc's poll via Syslib dlsym + the cosmoLibcCall6 trampoline. struct
pollfd and POLLIN/OUT/ERR/HUP values are identical on Linux and Apple
(AIX is the odd one), so no event translation. Level-triggered means
pollWait must arm each wait: netpoll.go grew a netpollLevelTriggered
bool (set only by the darwin poller's init) alongside the constant GOOS
list, and netpoll() disarms a direction when it delivers readiness.
netpoll(0) returns empty like AIX: a nonblocking check would still need
xnuMtxset, which the blocked poller holds - sysmon must not wait on it.
amd64 has no Syslib, so its poller stub reports unsupported and
netpollinit fails with a clear message (Intel-mac execution is dead
anyway until its runtime bring-up).

**Sockets** (socket_cosmo_arm64.go, dispatcher slow path): socket,
socketpair, bind, listen, accept(4), connect, getsockname/getpeername,
sendto/recvfrom, set/getsockopt, shutdown via dlsym'd libc. All
translation in one file: sockaddr Linux u16-family <-> Apple
{sa_len,sa_family} (payloads coincide for AF_UNIX/INET/INET6; AF_INET6
10<->30; abstract unix names refused EINVAL), SOCK_CLOEXEC/NONBLOCK
stripped and fcntl'd (accept4 = accept + fcntl; Apple has no accept4),
and a curated sockopt table (SOL_SOCKET 1->0xffff, SO_REUSEADDR 2->0x4,
SO_ERROR 4->0x1007 with the VALUE errno-translated too, SO_LINGER ->
SO_LINGER_SEC because Apple's plain SO_LINGER counts ticks, TCP
keepalive 4/5/6->0x10/0x101/0x102, IPV6_V6ONLY 26->27; unknown options
ENOPROTOOPT rather than programming whatever Apple option shares the
number). Every socket gets SO_NOSIGPIPE - no runtime SIGPIPE handler
exists on macOS until the signal wave, so a peer-closed write would
kill the process. sendmsg/recvmsg stay ENOSYS (msghdr field widths
differ; nothing in basic TCP/UDP needs them).

**os/exec** (exec_cosmo_arm64.go): pipe2 (pipe+fcntl), dup3
(dup2+F_SETFD, oldfd==newfd EINVAL), setsid, setpgid, execve (direct;
argv/envp shapes agree), wait4 (WCONTINUED 8->0x10; status passthrough
- encodings agree, embedded signal numbers stay Apple's until the
signal wave; rusage fixed up IN PLACE: both systems' struct rusage is
144 bytes and only the two int32-vs-int64 tv_usec fields differ, which
avoids a nosplit-busting local). Fork-child path (dup3, fcntl, chdir,
setsid, setpgid, execve, close, write) uses only pre-resolved pointers
to async-signal-safe libSystem functions, nosplit end to end. waitid
deliberately NOT emulated: blockUntilWaitable falls back cleanly on
ENOSYS to blocking wait4 = upstream darwin behavior (wait_unimp.go).
Chroot/credential/ctty options remain ENOSYS via the status pipe.
execve of a PRISTINE APE is ENOEXEC on XNU (OS limitation - cosmo libc
solves it inside its own execve wrapper, we don't); assimilated
Mach-O/ELF children exec directly, and the probe demonstrates the
sniff-the-magic-and-use-/bin/sh pattern.

**Nosplit lesson #3** (extends wave 4+5's two): the emulation budget is
tight enough that HELPER NESTING is a real cost - each Go call level is
~80-96 bytes (frame + outgoing args). The linker's nosplit check failed
the first socketpair/wait4 versions; fixes were flattening (apply fd
flags and SO_NOSIGPIPE sequentially at call sites, not nested), calling
darwinLibcCall6 directly where darwinCall's convenience frame didn't
fit, and the in-place rusage conversion. Read the check's output
top-down; it prints the exact chain and byte counts.

**arm64 Stat_t layout** (P0, pre-existing): ztypes_cosmo_arm64.go
carried amd64's struct stat; the arm64 kernel writes Mode at 16 (not
24), 32-bit Nlink/Blksize, Rdev at 32. Everything between Ino and Rdev
was scrambled on arm64-Linux hosts, masked by Size/Ino/timestamps
sharing offsets - and a zero Mode has no type bits, so IsRegular()
accidentally PASSED. Now matches upstream ztypes_linux_arm64.go; the
darwin emulation's mirror struct writes the same layout. Probe canary:
statdir (a directory must IsDir - a regular file can't catch this).
Verified under qemu-aarch64.

**cmd/go fat coverage**: go install now fattens (rerun of the ORIGINAL
install args against a scratch GOPATH - keeps pkg@version working,
which can't be rewritten to go build - with GOMODCACHE untouched and
GOBIN cleared; cross-compiled installs land in bin/GOOS_GOARCH/).
Plain `go build` without -o already fattened (single-main-package
builds default cfg.BuildO at build.go:475 - the earlier "never fattens"
diagnosis was wrong; multi-package -o-less builds discard binaries by
design). go test (-c) stays thin deliberately: host-run throwaway
artifacts, and fattening triples test compiles. -tags=faketime forces
thin: the windows sibling can't compile (time_fake.go is !windows and
the tag drops time_nofake.go), and thin beats failing the build.
[Obsolete 2026-07-18: the windows sibling build is gone, so the
faketime special case is removed and -tags=faketime builds fat.]

**Probe** (now 30 checks): + sleep (wall-clock bounded), ticker, after,
ctxtimeout, tcplisten/tcpecho/tcpserver, deadline (read deadline in the
past against a held-open conn -> i/o timeout), udp (loopback datagram),
execchild (self-exec through the full os/exec stack, launch mode chosen
by binary magic: ELF/Mach-O direct [windows direct mode removed
2026-07-18 with the PE payload], pristine APE via /bin/sh),
statdir. Plus a spin-loop watchdog (60s) that depends on nothing under
test - a wedged probe fails CI in a minute, not at the job timeout.

Still broken on macOS (wave 7+):
- Signals: rt_sigaction success-stub, no darwin sigtramp, Linux vs
  Apple signal numbers untranslated (also: wait statuses carry Apple
  termsig numbers; SIGPIPE handled per-socket via SO_NOSIGPIPE until
  real signal handling exists).
- sendmsg/recvmsg (fd passing, ReadMsg*) - needs msghdr translation.
- getdents64 (directory listing) - Apple has no Linux-layout
  getdirentries; needs a real emulation with dirent rewriting.
- Intel mac execution (runtime bring-up: clone/futex ENOSYS etc).

Pre-existing, noticed while verifying (NOT wave-6 regressions; present
at the wave-5 baseline on linux/amd64): net tests
TestUnixConnLocalAndRemoteNames/TestUnixgramConnLocalAndRemoteNames
(autobind abstract-name expectations) and TestBuffers_WriteTo/Copy
(writev accounting vs /dev/null) fail under GOOS=cosmo on Linux.

**Wave 6 postscript - kill(2) + the macOS CI wedge**: after the feature
commits were green, three macOS test jobs nondeterministically wedged
in their first go-test step (the SAME code passed in the runs between
them). Facts established: the wedge survives runner-side step timeouts
(timeout-minutes never fired; jobs sat 30+ min), survives run
cancellation, module prefetch is NOT the cause (a dedicated bounded
step completes instantly), and it even survived an in-step
process-group SIGKILL - i.e. a process stuck in an uninterruptible XNU
kernel state, most plausibly runner-infra weather around first-exec of
downloaded binaries (loader cc compile / Gatekeeper). Since it is
unkillable and produced zero logs, the countermeasures make any
recurrence self-terminating and self-documenting rather than silent:
kill(2) is now emulated on darwin (dlsym kill + Linux->Apple signal
table, delivery side only - so os.Process.Kill genuinely works there;
SIGKILL is kernel-enforced), the probe bounds its exec child itself
(30s then Kill), apetest wraps every binary in a 3-minute
exec.CommandContext with WaitDelay, CI wraps go test in
with-deadline.sh (process-group killer that ABANDONS SIGKILL-immune
corpses and routes output through a file so a corpse cannot hold the
runner's log pipe), and every job/step carries an explicit
timeout-minutes sized from observed green durations. Two consecutive
fully-hardened runs then passed all 6 jobs.

## 2026-07-02: Wave 7 - darwin getdents64; unnamed unix sockaddr; writev test

**getdents64 on darwin** (the last big file-syscall gap): os.ReadDir,
filepath.WalkDir and os.RemoveAll now work on macOS hosts. Design
decision - two candidate emulations:

(a) libc opendir/readdir/closedir needs a fd->DIR* table (the Linux
    syscall is plain fd-based) that goes stale across dup/lseek/close;
    Go's own darwin port simulates Getdirentries this way per call
    (openat(fd,".") + fdopendir + skip counting, storing the entry
    COUNT in the fd offset) - O(n^2), and its huge Apple dirent locals
    are unusable inside our nosplit budget anyway.
(b) Apple's raw __getdirentries64 syscall wrapper: fd-offset-based
    exactly like Linux getdents64, so lseek rewind and dup'd-fd
    semantics carry over with zero userspace state. CHOSEN.

__getdirentries64 is "private API" in App-Store terms (Go dropped its
static import in 2019 for that reason), but it is still exported from
libSystem - current xnu's own userspace tests (tests/vfs/o_search.c,
tests/extended_getdirentries64.c) declare and link it - and we resolve
it via the Syslib's dlsym at startup like every other emulation
symbol, so a hypothetical future removal degrades to a visible ENOSYS,
not a crash.

Record rewrite: Apple {ino u64, seekoff u64, reclen u16, namlen u16,
type u8, name...} -> Linux {ino u64, off i64, reclen u16 @16, type u8
@18, name @19, NUL-terminated}, reclen recomputed with 8-byte
alignment, IN PLACE in the caller's buffer: the Linux record is never
longer than the Apple record for the same name (19- vs 21-byte header,
both 8-aligned), so a forward rewrite can't overrun unread input and
partial-record truncation is impossible - the buffer-space edge cases
of a two-buffer scheme never arise. Header fields go through locals
before the destination header is written (dst can equal src);
malformed records are EIO, not guesses. d_type passes through:
DT_UNKNOWN/FIFO/CHR/DIR/BLK/REG/LNK/SOCK/WHT = 0/1/2/4/6/8/10/12/14 on
BOTH systems (checked value by value; shared BSD lineage). xnu quirk
(bsd/sys/dirent_private.h): bufsize >= 1024 makes the kernel reserve
the buffer's last 4 bytes for a GETDIRENTRIES64_EOF flags word - only
shrinks per-call capacity; the flags land beyond the returned length.
Whole path nosplit (callers are inside the _Gsyscall window), no big
locals needed, linker check green. Probe: readdir (names+IsDir bits,
sorted), walkdir (typed visit counts), removeall (lists directories
itself).

**Unnamed unix sockets parsed as "@"** (pre-existing; failed net's
TestUnix{Conn,gramConn}LocalAndRemoteNames on Linux): cosmo's
anyToSockaddr, copied from the Linux port, rewrote a leading sun_path
NUL to '@' unconditionally - an UNBOUND socket (dialers, socketpair
ends; the kernel returns just the 2-byte family) became the abstract
name "@", which does not exist and cannot be dialed. net's tests
expect that display quirk only for android/linux/windows; every other
GOOS must report "". Root cause is that the Linux-shape sockaddr has
no in-band length and anyToSockaddr's shared signature (syscall_unix.go
callers) doesn't provide the socklen - but every caller passes a
pre-zeroed buffer, so unnamed (path all zero) and abstract (>=1
nonzero byte) are separable by content. All-zero now parses as ""; the
abstract branch is untouched (verified: "@name" bind/dial/getsockname
round-trips on Linux). This is also automatically right on macOS,
where Apple zero-fills the sockaddr for unnamed sockets and the
abstract namespace doesn't exist. Probe: unixsock (pathname listener
addr round-trip + unnamed-dialer canary; the windows payload expected
"@" there per net's own unixsock tests, a branch removed 2026-07-18
with the payload) and unixecho - which also gives the
wave-6 AF_UNIX socket emulation its first macOS CI coverage.

**arm64 O_DIRECTORY/O_NOFOLLOW were the amd64 kernel's numbers**
(pre-existing, found by the new readdir probe the first time it ran
under qemu-aarch64): zerrors_cosmo_arm64.go said O_DIRECTORY=0x10000,
O_NOFOLLOW=0x20000 - the x86-64 kernel's values. arm64 uses the
asm-generic numbers (0x4000/0x8000) and reads the amd64 bits as
O_DIRECT/O_LARGEFILE, so on arm64 Linux hosts every O_DIRECTORY open
(os.ReadDir's openDirNolog, os.RemoveAll's openDirAt, os.Root) was
really an O_DIRECT open and failed EINVAL on tmpfs. Fixed the two
constants and flipped the darwin openat translation table
(cosmo_xlat_oflags_r2 bits 14/15 -> Apple O_DIRECTORY/O_NOFOLLOW, bits
16/17 = generic O_DIRECT/O_LARGEFILE stripped). The wave-4 table's own
comment had recorded "kernel-arm64 O_DIRECTORY/O_NOFOLLOW - unused by
this port's userspace" - the assumption half of that sentence was the
bug. Full probe green under qemu-aarch64 and on linux/amd64 after.

**TestBuffers_WriteTo "writev call sum = N; want 0"** (pre-existing):
NOT a writev bug - the byte log summed to exactly the payload, i.e.
internal/poll.FD.Writev worked perfectly on cosmo all along. The
test's GOOS switch simply had no cosmo entry, so it expected writev to
be unused. Added cosmo to the unix branch (sum == payload,
ceil(chunks/1024) minimum calls - the same iovec batching as Linux).

Still broken on macOS (wave 8+ candidates):
- Signals (unchanged from wave 6): rt_sigaction success-stub, no
  darwin sigtramp, Linux-vs-Apple signal numbers untranslated.
- sendmsg/recvmsg: still deliberately ENOSYS. The full fix needs
  msghdr translation (Linux iovlen/controllen are 64-bit where Apple's
  are 32-bit, field order shifts) AND cmsghdr rewriting (Linux cmsg_len
  is u64, Apple's u32; alignment 8 vs 4), both directions, fd-array
  payloads preserved - deferred whole rather than half-landed.
- writev/readv on darwin: not routed in the emulation (honest ENOSYS).
  struct iovec is identical on both systems, so this is a candidate
  trivial dlsym passthrough next wave; today net.Buffers fails on
  macOS hosts only.
- Intel mac execution (runtime bring-up: clone/futex ENOSYS etc).

## 2026-07-02: Wave 8 - signal delivery on macOS ARM64; darwin netpoller wedge fixed

Signals went from fully stubbed (rt_sigaction fake-success: SIGSEGV =
dead process, no os/signal, no async preemption - tight loops hung
GC/STW forever, wait statuses carried Apple numbers) to CI-verified
working on macos-latest: sigpanic + recover, os/signal delivery,
SIGURG async preemption, and Linux-numbered wait statuses. Linux paths
are byte-for-byte untouched (runtime dispatch on __hostos throughout).

**Number/mask translation** (sigxlat_cosmo.go + cosmo pkg sig_cosmo.go):
the runtime thinks in LINUX signal numbers everywhere (sigtable, _SIG*,
syscall, os/signal); Apple diverges over the BSD range (SIGBUS 7 vs 10,
SIGUSR1 10 vs 30, SIGCHLD 17 vs 20, SIGURG 23 vs 16, SIGSYS 31 vs 12,
...; SIGSTKFLT/SIGPWR/rt-range have no Apple number, SIGEMT/SIGINFO no
Linux one). Full 1..31 tables both directions, derived from upstream
defs_linux_arm64.go vs defs_darwin_arm64.go, as plain-data byte arrays
indexable from asm; sigsets are bit-REMAPPED through them (Linux 8-byte
mask bit N-1 <-> Apple 4-byte mask bit M-1), not width-truncated.
Unit tests pin both packages' tables to one literal pair list and
verify inverse/round-trip/unmapped/out-of-range and mask remaps.
Translate Linux->Apple when installing/masking/sending (sigaction,
pthread_sigmask, kill, pthread_kill, raise, tgkill, sigfwd),
Apple->Linux when receiving (sigtramp) and when decoding wait statuses
(darwinWait4; encodings agree, only embedded signal numbers rewritten).

**Install machinery** (signal_cosmo_xnu.go): the Syslib's sigaction is
a sysret-wrapped passthrough to Apple LIBC sigaction (verified in
ape-m1.c), so it takes the libc struct {handler, mask u32, flags i32}
(= upstream usigactiont; libc adds its own kernel trampoline and calls
sigreturn when our handler returns - our sigtramp just RETs, exactly
like upstream darwin). Flag VALUES translate (SA_SIGINFO 0x4->0x40,
SA_ONSTACK 0x8000000->0x1, SA_RESTART 0x10000000->0x2); unmapped
signals no-op cleanly so initsig/setsigstack/clearSignalHandlers stay
oblivious. sigprocmask -> pthread_sigmask with how+1 (Linux 0/1/2 ->
Apple 1/2/3; arm64 counterpart of the wave-4 amd64 fix) and remapped
4-byte sets. sigaltstack translates Linux arm64 stackt {sp,flags,pad,
size} <-> Apple {sp,size,flags} and SS_DISABLE 2<->4; the 32KiB
gsignal stack meets Apple arm64's MINSIGSTKSZ exactly. All of it
nosplit/lock-free (setsig runs inside dieFromSignal on the signal
stack; clearSignalHandlers + msigrestore run between fork and exec).
amd64 keeps its raw-XNU stubs behind GOARCH guards (Intel-mac runtime
bring-up still pending).

**Receive side**: sigctxt is HOST-AWARE rather than translated - the
kernel hands the handler its native context (Linux ucontext embeds the
mcontext; Apple's uc_mcontext is a POINTER at offset 48 into the
signal frame, per upstream defs_darwin_arm64.go), so every accessor
dispatches on __hostos and reads/writes the real structure. Writes
(set_pc/set_sp for sigpanic injection, pushCall for preemption) land
in the kernel's own mcontext, so Apple's sigreturn restores them for
free - no copying, nothing to write back. Apple siginfo si_addr sits
at 24 (Linux union at 16); si_code at 8 on both, so sigcode needs no
dispatch. _SI_USER=0 matches Apple's empirical kill() si_code (same
note as upstream os_darwin.go). sigtramp translates the incoming
Apple number through the byte table before any Go runs and ignores
unmapped ones; sigfwd hands foreign handlers the APPLE number next to
the untranslated Apple info/ctx. fixsigcode ports upstream's darwin
SIGTRAP breakpoint correction.

**Preemption/sending**: m.procid was gettid()'s uint32 truncation of
pthread_self - every pthread_kill aimed at a garbage handle. minit now
stores the full pthread_t on XNU; signalM translates and pthread_kills
it. raise/raiseproc/tgkill translate in asm (unmapped -> signal 0,
a no-op probe). dieFromSignal therefore kills with the CORRECT Apple
signal and the wait status decodes truthfully on the other side.

**Netpoller wedge: bounded, with the honest diagnosis** (the wave-6/7
mystery; 8 watchdog kills across this wave's CI pushes, every one a
goroutine inside a mutator path - net.Listen/Dial -> netpollopen,
pollWait -> netpollarm - stuck on xnuMtxset while the poller slept in
poll(2)). Two theories were worked through ON CI EVIDENCE:

(1) Poller barging (commit 0c8dfef8, RETRACTED in abf7db49): the idea
    that the unfair runtime mutex lets the hot poller re-take
    xnuMtxset ahead of the semawakeup'd mutator after its byte was
    drained. Wrong: the AIX protocol already prevents it - a mutator
    HOLDS xnuMtxpoll while queued on xnuMtxset, so the poller's next
    cycle blocks at lock(xnuMtxpoll) until the mutator is through.
    The counter/yield "fix" is removed.
(2) What the watchdog traceback actually shows (the panic watchdog
    earned its keep on its first firing): main _Grunning inside
    netpollopen's lock(xnuMtxset) - the mutator side executed the
    wakeup protocol correctly (flag was clear after the cycle-top
    reset, so its byte WAS written) - and a second goroutine parked
    normally; the poller never came out of poll(2). With the
    userspace protocol re-derived sound twice, what remains is the
    KERNEL losing the pipe wakeup - Apple's poll() has a
    long-documented race of exactly this shape (rdar 37537852,
    "poll() sometimes doesn't return when a polled pipe becomes
    readable"; the reason darwin software generally uses kqueue).
    Load/timing dependence matches the nondeterminism across
    identical-code runs.

Mitigation, since the kernel cannot be fixed from here: never trust
an unbounded wait - every blocking poll is capped at 100ms
(xnuMaxPollMs). A lost wakeup now costs <=100ms (poller times out,
returns empty, RELEASES xnuMtxset - freeing any stranded mutator -
and the scheduler re-enters with a recomputed delay) instead of
wedging until an external kill. Real readiness is unaffected
(level-triggered); an idle process wakes 10x/s on one thread.
Additional hardening for the signal era: the wakeup-pipe writers now
retry EINTR (SIGURG preemption interrupts libc calls; an unretried
EINTR behind the once-only guards would lose the byte on the USER
side) and treat EAGAIN as delivered, mirroring the epoll eventfd
loop; write1/read's darwin paths translate their -errno results
Apple->Linux so those comparisons work at all. The probe watchdog
panics with traceback "all" instead of calling runtime.Stack:
Stack(all) needs a stop-the-world, which an M wedged inside a runtime
lock blocks forever (a CI run proved it - the dump hung for the
step's remaining budget), while fatalpanic's freezetheworld is
best-effort and dumps mutex-wedged Ms without their cooperation.

**Wedge postscript (end of wave)**: after the poll cap, the wedge
recurred with an UNCHANGED traceback - mutator asleep in
lock(xnuMtxset) although the capped poller now provably releases that
mutex every cycle and unlock2Wake's decision logic must wake the sole
queued waiter (no spinner flag, no stack lock, sleeping bit set). A
further mitigation - semasleep(-1) waiting in 50ms slices, each a
FRESH dispatch_semaphore_wait that atomically re-examines the count
(semantics identical: still returns only on genuine wakeup, which
lock2's wait-list invariant requires) - did not cure it either. That
eliminates, in order: poll-level loss (bounded), wake-decision logic
(read, sound), wait-side-stuck loss (sliced). What remains is the
signal side of the dispatch-semaphore pair failing to increment the
target M's semaphore (or an M-identity issue below mutexWaitListHead
that code reading says cannot happen). Upstream darwin pointedly
parks Ms on pthread_mutex+pthread_cond rather than dispatch
semaphores; REPLACING the parking primitive with dlsym'd pthread
mutex/cond, exactly upstream's design, is the concrete wave-9
recommendation - too invasive to land safely from a Linux box at the
end of this wave. Status: pre-existing wave-6 defect, nondeterministic
(load-dependent; several all-6-green runs today on this exact code,
including head run 28584207120), bounded in blast radius by the
watchdog + CI step killers, and fully instrumented - every future
occurrence prints the all-goroutine traceback.

**Also landed**: readv/writev dlsym passthrough on darwin (iovec
identical; unblocks net.Buffers on macOS - the wave-7 note was right
that it is trivial). Probe grew segvrecover, sigterm+sigusr2 (USR2's
diverging number proves install- and receive-side translation agree),
preempt (GOMAXPROCS call-free spin loops + full GC within 10s;
bounded iterations so broken preemption fails by duration instead of
hanging), waitsig (child kills itself with SIGKILL - uncatchable,
hence deterministic; Go discards un-notified catchable signals and
converges fatal paths to SIGABRT, which is why the plan's original
USR1 idea cannot work - parent asserts Signaled()+SIGKILL through the
wait4 translation; skip-lines on windows). The probe is now a
multi-file package (per-OS signal helpers; syscall.Kill/SIGUSR2 don't
exist for the windows payload), so CI builds its module directory.
[2026-07-18: the windows signal half and its skip-lines left with the
payload; the probe remains a multi-file module.]

**Verified**: make.bash; vet clean both arches; full probe green on
linux/amd64 AND qemu-aarch64 (40 lines incl. the new five; per-binary
ELF-header assimilation - a binary must be stamped with its OWN
embedded printf header, not another build's); apetest suite green;
stdlib under cosmo on Linux: os/signal ok (full suite), net ok, time
ok, os ok, os/exec ok - no regressions vs the wave-7 baseline. Head
CI run 28584207120 (abf7db49): ALL 6 JOBS GREEN; on macos-latest the
full probe passed for all three origin binaries, including the new
`ok segvrecover`, `ok sigterm`, `ok sigusr2`, `ok preempt` (~140ms-
600ms) and `ok waitsig killed`. (The first attempt of that run lost
the macOS BUILD job to runner DNS - "Could not resolve host:
github.com" at checkout - unrelated to the code; the rerun was clean.
The macos runner pool was visibly unhealthy all day, which also
inflated the wedge recurrence rate that finally made it fixable.)

Still missing on macOS hosts (deferred, in dependency order):
- sendmsg/recvmsg (msghdr/cmsghdr translation; fd passing, ReadMsg*).
- setitimer/SIGPROF profiling (itimerval layouts differ; SIGPROF
  delivery would work now, the timer plumbing is what's absent).
- AllThreadsSyscall (sigPerThreadSyscall is Linux-rt-range; unused by
  the stdlib on cosmo today; darwinSignalM drops it).
- FPE si_code values differ (Apple FPE_INTDIV=7 vs Linux 1): only
  affects the panic message for float traps, which arm64 does not
  generate by default; upstream darwin/arm64 leaves it too.
- Intel-mac runtime bring-up (clone/futex/sigaction; amd64 stubs
  unchanged and honestly gated).

## 2026-07-02: Wave 9 - wedge ROOT-CAUSED and killed: XNU pipe read; kqueue netpoller; pthread M-parking

The macOS CI wedge (waves 6-9: goroutines stuck in netpollopen/
netpollarm on xnuMtx locks, ~50-65% of macOS test jobs on bad days) is
dead, and not by mitigation: the root cause was isolated to a single
kernel-side syscall misbehavior by in-CI instrumentation, and the
subsystem that depended on it was replaced with upstream darwin's
design. Three pieces landed, in order:

**1. pthread M-parking** (f922bbfc, prepared by 104ed6af): XNU-host M
parking moved from Syslib dispatch semaphores to upstream
os_darwin.go's design, ported field for field - per-M pthread_mutex +
pthread_cond guarding a count (mOS.initialized/mutex/cond/count;
pthread_mutex_t = int64 sig + 56 opaque bytes = 64, pthread_cond_t =
int64 sig + 40 = 48, 8-aligned via the leading int64, from upstream
defs_darwin_arm64.go), dlsym'd entry points, semasleep looping on
count<=0 with pthread_cond_timedwait_relative_np for timed waits
(Apple ETIMEDOUT=60 - raw libc returns are Apple-numbered; fallback
to absolute pthread_cond_timedwait via walltime if the _np symbol
ever vanishes - it resolved fine on every CI run). The calls run
through asmcgocall + ABI0 trampolines (cgo_unsafe_args block,
upstream sys_darwin.go's exact libcCall pattern minus the
libcallpc/sp profiler bookkeeping - SIGPROF timers are not wired on
XNU yet): pthread functions have real C frames, and contended lock2
parks from user g stacks, so the g0 switch is mandatory. Because
pthread_mutex_lock is not async-signal-safe (dispatch_semaphore_signal
accidentally was), sigqueue's sigsend can no longer notewakeup on
XNU: 104ed6af first ported upstream's pipe-based sigNote
(usesSigNote() in sigqueue.go - constant on darwin/ios, host-checked
on cosmo, false elsewhere; EINTR-retried wakeup write; runtime.read
gained a truthful go:noescape). semasleep/semawakeup throw on the
gsignal stack exactly like upstream. Fork-child needs no reinit (the
fork->execve path is nosplit and lock-free by construction). Linux
futex parking is byte-for-byte untouched. The wave-8 50ms-slice
mitigation died with the primitive.

**2. The forensic loop that found the real bug** (fb3dea5d, 77f8679c,
5d4ebbc1): the wedge promptly recurred ON the pthread commit (run
28589956528) with the same traceback shape, proving the parking
primitive was never the cause and wave 8's remaining suspect wrong.
Since the poller M runs on g0 and is invisible in goroutine dumps,
the poller got progress counters exported through
runtime.cosmoNetpollDiag, and the probe's watchdog prints two samples
300ms apart when it fires - each wedge capture then narrowed the
search without a reroll:
- Round 1 (fb3dea5d, run 28591228556, 3 wedged steps): poll cycles
  FROZEN with the last poll(2) returned n=1/errno 0 ~90s earlier and
  a mutator wakeup pending. The kernel's poll TIMEOUT path and the
  wave-8 100ms cap were working; "stuck inside poll(2)" eliminated.
- Round 2 (77f8679c, runs 28592344544/28592353386/28592365698, 4
  wedged steps): done==cycles-1 (poller stuck MID-cycle holding
  xnuMtxset), mutators correctly queued (mut=E/E-1/E-1), and
  semawake/acq EQUAL and ADVANCING throughout - M parking (pthread
  and, retroactively, dispatch) delivers wakeups fine during the
  wedge; lock_spinbit's wake decisions sound. Only the poller's
  post-poll bracket remained.
- Round 3 (5d4ebbc1, runs 28593361662/28593341482/28593350164, 5
  wedged steps): phase markers name the statement - phase=2,
  drainReads one past the last completed read, drainLastRet=1: the
  poller is inside the wakeup-pipe drain's final read(2) - a
  nonblocking read of an empty pipe - and that syscall NEVER RETURNS
  (89.8s and counting at watchdog time), despite the same fd draining
  nonblockingly moments earlier in the same process. runtime.read's
  darwin path is a straight trampoline into Apple libc read, so the
  hang is inside XNU. Same haunted pipe machinery as the documented
  poll-on-pipe race (rdar 37537852) that wave 8's cap defended
  against; the read side is broken too, and no userspace cap can
  bound a syscall that does not return.

**3. The kill: kqueue netpoller** (5fce773d): the aix-shaped
poll+pipe poller was replaced with a port of upstream
netpoll_kqueue.go + netpoll_kqueue_event.go - what GOOS=darwin itself
runs, on the kernel mechanism Apple actually maintains. kqueue/kevent
via the established dlsym machinery (honest ENOSYS stubs on amd64);
EV_ADD|EV_CLEAR edge-triggered registration at netpollopen with the
tagged pd pointer in udata; EVFILT_USER + NOTE_TRIGGER for
netpollBreak; kevent ETIMEDOUT tolerated (go.dev/issue/59679); >1e6s
timespec clamped (darwin EINVAL). Gone wholesale: the self-pipe, the
drain loop, xnuMtxpoll/xnuMtxset, level-triggered arming
(netpollLevelTriggered now stays false; netpollarm unreachable), the
netpoll(0)-returns-nothing limitation, the 100ms poll cap - and the
runtime's only instance of holding a runtime mutex across a blocking
syscall. The forensic counters stay (three atomic stores per kevent
cycle + the parking counters): any future poller stall names itself
in the watchdog's CI log the way this one did.

**Wedge verdict**: CONFIRMED root cause - XNU sporadically never
returns from a nonblocking read(2) on a pipe under load (observed on
macos-latest arm64 runners). Not the dispatch semaphores (wave-8's
remaining suspect: parking counters stayed healthy through wedges on
both primitives), not the poll timeout (the cap fired correctly), not
lock_spinbit, not the userspace protocol (re-derived sound and its
counters agreed). The pthread parking port stays on its own merits:
upstream parity, and it removed the accidental dependence of sigsend
on a signal-safe semaphore.

**CI evidence**: with the kqueue poller at 5fce773d, SIX consecutive
fully-green full-matrix runs - 28594244120 (push), 28594284723,
28594294538, 28594760519, 28594769929, 28594779821 - i.e. 18/18
consecutive macOS probe executions green, on the same afternoon and
runner pool where the three capture rounds immediately prior wedged
3, 4 and 5 steps out of every 9 (per-step wedge probability ~33-55%).
P(18 straight greens | unchanged wedge) < 0.001 even at the lowest
observed rate - and unlike a statistical argument alone, the fix
removed a directly observed failing syscall.

**Verified locally** (Linux paths - epoll and futex - untouched by
the darwin work): make.bash; go vet runtime clean for cosmo
amd64+arm64; fat fizzbuzz + probe 40/40 + "ok all" on linux/amd64 AND
under qemu-aarch64 (per-binary arm64 ELF header stamp); apetest green
against the kqueue-build binaries; stdlib under cosmo on Linux:
os/signal, net, time, os, os/exec all ok.

Still missing on macOS hosts (unchanged from wave 8):
- sendmsg/recvmsg (msghdr/cmsghdr translation; fd passing, ReadMsg*).
- setitimer/SIGPROF profiling (also why the pthread wrappers skip
  upstream's libcall pc/sp bookkeeping; add both together).
- AllThreadsSyscall (rt-range unmapped; unused by stdlib on cosmo).
- Intel-mac runtime bring-up.


## 2026-07-03: go1.26.4 uprev - upstream merge + two cosmo build fixes

Merged upstream tag go1.26.4 (previous base was a Jan-6 pre-1.26.0 dev
snapshot; 161 upstream commits, including the os.Root symlink-escape,
pkg-config sanitization and cgo trust-boundary security backports).
Single conflict: VERSION (resolved to go1.26.4cosmo). The four
overlap files with cosmo edits (cmd/go work/exec.go APE magic,
internal/syscall/unix/at.go, os/dir_unix.go, os/file_unix.go)
auto-merged with the cosmo edits intact.

Two GOOS=cosmo build breaks from upstream refactors, both fixed:

1. **unix.Fchmodat undefined**: 1.26.4 moved Fchmodat out of at.go
   into fchmodat_linux.go / fchmodat_other.go (os.Root hardening);
   neither tag covered cosmo. New fchmodat_cosmo.go: flags==0 goes
   straight to the 3-arg Linux-ABI fchmodat; AT_SYMLINK_NOFOLLOW uses
   the glibc/musl O_PATH + /proc/self/fd re-chmod workaround and
   refuses symlinks with EOPNOTSUPP, because the Linux fchmodat(2)
   ABI silently ignores the flag - passing it through would chmod the
   symlink target, the exact escape the upstream security fix closed.
   Procfs-less hosts (macOS) get EOPNOTSUPP; the darwin emulation has
   no chmod family yet anyway.
2. **f.lstatatNolog undefined**: new statat_unix.go build tag lacked
   cosmo; added (pfd.Fstatat + AT_SYMLINK_NOFOLLOW already work).

**Verified**: make.bash (go version go1.26.4cosmo linux/amd64); host
cmd/link+cmd/go reinstall; GOOS=cosmo go build std clean on
amd64+arm64; vet clean for internal/syscall/unix + os both arches;
fat fizzbuzz correct + probe 40/40 "ok all" on linux/amd64; thin
arm64 probe 40/40 under qemu-aarch64 (own-header stamp); apetest 118
PASS; stdlib under cosmo on Linux: time, os, os/exec, os/signal, net
all ok (os needs the APE binfmt_misc handler for
TestExecutableDeleted, the known wave-3 host limitation - registering
`:APE:M::MZqFpD::/bin/sh:` makes the suite fully green).


## 2026-07-05: one-off macOS CI wedge datapoint (PR #24 head 93fe6770)

Logging a single nondeterministic wedge so the pattern is trackable if
it recurs. On 2026-07-04 ~23:24 UTC, the `test (macos-latest)` job on
PR #24 (head 93fe6770) wedged inside the "Test binary built on Ubuntu"
step - apetest executing the ubuntu-origin fat APE on the macOS ARM64
runner. It survived BOTH kill layers: the step-level timeout-minutes: 5
AND the in-step with-deadline.sh 240s killer. Only the job-level 25m
timeout ended it, and because the runner was torn down at that level,
the job log was never flushed and no artifact was uploaded - zero
on-disk evidence of where it stopped.

**Nondeterminism**: the IDENTICAL SHA with identical binaries passed
the same step in 37s on a warm-branch run minutes earlier and in 49s
on a re-run minutes later. One wedge bracketed by two fast greens on
the same runner pool is the signature of the wave-9 family (XNU
sporadically parking a call that "cannot" block), not of a
deterministic regression in the commit.

**Open question**: runner infra flake vs a rare residual darwin-cosmo
runtime race. The wave-9 kqueue/pthread rework removed the one
directly observed wedge mechanism (nonblocking read(2) on the poller
wakeup pipe never returning) and bought 18/18 consecutive green probe
executions, so if this is ours, it is a second, much rarer mechanism -
one datapoint is not enough to distinguish.

**Next instrumentation if it recurs**: sentinel-file hardening in
with-deadline.sh. The current design can be defeated if the step's
shell itself wedges - the watchdog only helps when the kill fires and
the shell survives to flush output. Write a sentinel file (step name,
phase, pid, timestamp) to the workspace BEFORE launching the guarded
command and update it from the watchdog, so a wedged shell still
leaves on-disk evidence for the artifact upload of a later step (or a
job-level always() uploader) to collect.

## 2026-07-18: embedded windows/amd64 PE payload removed from fat APEs

Fat APEs now contain exactly two images - cosmo amd64 + cosmo arm64 -
instead of three. The GOOS=windows sibling build in cosmoFatten /
cosmoFattenInstall is gone, `-apefat` takes exactly two inputs, and
cmd/link's PE-transplant machinery (pePayload, payloadFromPE,
peHeaderOffset, writePEHeaderFromPayload, the `win` parameter threaded
through layoutAPE/writeAPEFile/makeAPEHeaderForPayloads) is deleted.

**Why**: size. The third image was a complete windows/amd64 Go binary,
about a third of every fat APE: fizzbuzz.com went 7602176 -> 5084226
bytes (-33.1%), runtimeprobe.com 11344384 -> 7305033 bytes (-35.6%).

**What Windows gets today**: exactly what thin GOCOSMOFAT=0 builds
have always shipped - the parseable stub PE header at 0x80
(writePEHeader, itself untouched), whose entry point is the 3-byte
xor eax,eax; ret at file offset 0x200. The file loads as a valid
console PE and exits 0 without running any cosmo code. Fat and thin
builds now emit the 0x80 region through that one writePEHeader path,
and apetest's pe_test.go is the stub's regression suite (the two
embedded-payload assertions became TestPETextSectionInsideStubHeader
and TestPEEntrypointIsPlaceholderStub).

**Replacement path (in progress)**: cosmo-native NT bring-up - boot
the cosmo amd64 image on Windows through the APE PE header plus an NT
personality in the cosmo runtime, vim.com-style. The runtime already
declares _HOSTWINDOWS (os_cosmo_amd64.go / os_cosmo_arm64.go) but
nothing references it yet; before this change the embedded PE payload
was the ONLY Windows execution path. Until the NT personality lands,
Windows execution is temporarily unavailable by design - being
reimplemented, not dropped.

**Cleaned up with it**: the faketime-forces-thin special case in
cosmoFatEnabled (the windows sibling was its only reason;
-tags=faketime now builds fat, verified by the apetest TestFat
structural suite against a faketime fizzbuzz build). The runtime probe
lost its dead windows halves (sig_windows.go, the unixsock "@"
expectation, windows direct-launch, the waitsig skip) and remains a
multi-file module. CI dropped the windows-latest test leg - its unique
value was executing the embedded PE - while the windows-latest build
leg stays for make.bat toolchain health and windows-origin cross-build
coverage, whose binaries the unix test legs execute.

# 2026-07-18: Windows (NT) bring-up — wave 1 design

## Goal and reference

North star: a normal fat APE - cosmo amd64 + cosmo arm64, no embedded
second build - boots and runs natively on Windows through its PE
header, the way real Cosmopolitan APEs (vim.com) do. Wave-1 target:
testdata/fizzbuzz prints correctly and exits 0 on a windows-latest
runner. This builds directly on the payload-removal branch (PR #45):
thin and fat now both emit the 0x80 region through the single
writePEHeader call (ape.go:496 -> writePEHeader ape.go:817), and that
one code path is what this wave upgrades from stub to real.

Reference architecture is jart/cosmopolitan: PE header construction in
ape/ape.S + ape/ape.lds, boot via the __msabi C function WinMain
(libc/runtime/winmain.greg.c:312 - the PE entry point IS WinMain,
ape.lds "ape_pe_entry = WinMain"), and genuine loader-resolved PE
imports composed in asm (ape/idata.internal.h: "The NT Executive fills
its value before control is handed off to the program"). Ground truth
from dissecting a prebuilt cosmo APE (hello.ape): Machine 0x8664,
PE32+, Characteristics 0x223 (RELOCS_STRIPPED|EXECUTABLE_IMAGE|
LARGE_ADDRESS_AWARE|DEBUG_STRIPPED), Section=FileAlignment=4096 with
RVA == file offset for every section, ImageBase 0x41F0000 = the link
base 0x4200000 minus a 0x10000 apelink skew (the base must stay a
multiple of 65536, apelink.c:782-783), SizeOfHeaders 0x14000 covering
the entire pre-.text prologue (shell script and all), 4 sections
(.text RX, .rdata R, .idata RW - the loader writes the IAT in place -
.data RW with VirtualSize > SizeOfRawData zero-filling BSS),
NumberOfRvaAndSizes=2 with only the import directory populated, stack
reserve/commit a deliberately tiny 65536/4096 (cosmo pivots onto its
own allocated stack at boot), and imports from exactly 5 DLLs
(kernel32 ~70 fns, advapi32, BCryptPrimitives ProcessPrng,
API-MS-Win-Core-Synch-l1-2-0 WaitOnAddress family,
API-MS-Win-Core-Realtime-l1-1-1 interrupt-time clocks). ntdll is
deliberately NOT in the import table - cosmo resolves it at runtime
via GetProcAddress with graceful not-found stubs so binaries never
fail to LOAD over a private API.

## PE container design (cmd/link)

- Keep e_lfanew=0x80. Header budget is [0x80,0x7FE] = 1919 bytes in
  BOTH thin and fat: the shell script is fixed at apeScriptOffset
  0x800 (ape.go:37) and 0x7FF is the forced newline before the heredoc
  terminator. PE32+, Machine 0x8664.
- Characteristics 0x0223 (RELOCS_STRIPPED|EXECUTABLE_IMAGE|
  LARGE_ADDRESS_AWARE|DEBUG_STRIPPED), matching real cosmo (the stub
  ships 0x22 today). DllCharacteristics: NX_COMPAT|
  TERMINAL_SERVER_AWARE only - the stub's DYNAMIC_BASE|HIGH_ENTROPY_VA
  (0x8160) must be dropped: there is no .reloc section and cosmo code
  is position-dependent, so ASLR must not be invited.
- ImageBase = 0x100000000 = the cosmo/amd64 link base, already
  64K-aligned (amd64/obj.go:105-116: HEADR=ELFRESERVE=4096, FlagRound
  4096, FlagTextAddr = Rnd(0x100000000, 4096) + HEADR). RVA =
  vaddr - ImageBase; PointerToRawData = ABSOLUTE file offset of the
  segment bytes. This works because the amd64 image lands at file
  offset 0x10000 with byte-identical content in thin and fat, and
  vaddr - fileoff is the constant 0xFFFF0000 for every PT_LOAD (the
  layout invariant Vaddr == Fileoff mod FlagRound is enforced at
  data.go:3183-3186; the constant delta was verified against the
  ground-truth fizzbuzz phdrs). So the PE header the thin link
  computes is valid VERBATIM in the fat container - the fat path
  transplants the same bytes at 0x80, no shifting of anything.
- SectionAlignment 0x1000, FileAlignment 0x200. Sections are built
  from the image's PT_LOADs, skipping the payload ELF-header page:
  .text RX (RVA 0x1000), .rodata R, .data RW with VirtualSize >
  SizeOfRawData covering BSS. Concrete example, the measured thin
  fizzbuzz phdr triple (absolute .com offsets): R E off 0x10000 vaddr
  0x100000000 filesz 0xaa2b1 / R off 0xbb000 vaddr 0x1000ab000 filesz
  0xe1d68 / RW off 0x19d000 vaddr 0x10018d000 filesz 0xa860 memsz
  0x2f208 (BSS tail). SizeOfImage = end of the last section rounded to
  0x1000; SizeOfHeaders = 0x400 (>= the real header end 0x80 + ~0x1A8
  + 3x40 of sections, <= the first section RVA, file-aligned - and
  note real cosmo's hard-won warning that SizeOfHeaders must be <=
  AddressOfEntryPoint, apelink.c:765-813, "complex and confusing
  requirements").
- Subsystem CONSOLE(3), Major/MinorSubsystemVersion 6.0,
  MajorOperatingSystemVersion 6.0. SizeOfStackReserve 8 MiB,
  SizeOfStackCommit 64 KiB: rt0_go assumes <= 64 KiB of g0 stack below
  the entry SP (asm_amd64.s:192-199, LEAQ (-64*1024)(SP)), and
  guard-page growth covers the rest of the reserve. Heap 1 MiB/4 KiB.
- Imports: a real loader-resolved import directory (the cosmo model),
  but MINIMAL: kernel32.dll -> GetProcAddress + LoadLibraryA only;
  everything else is resolved at runtime through those two (mirroring
  the darwin port's dlsym-at-osArchInit idiom, cosmoDlsym
  os_cosmo_arm64.go:170-176, resolution list :257-319). The
  IDT/ILT/hint-name/IAT bytes live INSIDE the amd64 image as
  fixed-layout runtime symbols: a runtime·ntidata blob plus a
  runtime·ntiat 2-slot array in the data segment - RW so the loader
  can write the IAT; real cosmo also keeps its .idata RW. The linker
  computes their RVAs at convertToAPE time via the Entryvalue/
  ldr.Lookup mechanism (lib.go:2890-2909; convertToAPE runs inside the
  live link at main.go:498, loader alive), writes the structures into
  the output file at those offsets, and points DataDirectory[1] at
  them. NumberOfRvaAndSizes stays 16 - it fits the budget: 4 + 20 +
  240 + 3*40 = 384 <= 1919.
- AddressOfEntryPoint = RVA of the new runtime symbol _rt0_cosmo_nt,
  looked up exactly like Entryvalue resolves the ELF entry
  (_rt0_amd64_cosmo per the _rt0_%s_%s convention, lib.go:415-420).
- arm64: no PE support - Windows/arm64 is out of scope; the fat PE
  targets the amd64 image only, and thin arm64 builds keep the
  harmless stub (writePEHeader's 0xAA64 leg).
- Structural tests: extend the ld-package ape_test.go suite (synthetic
  ELFs via buildTestELF driving the real emission code in-process,
  same pattern as checkMachoKernelInvariants) with a debug/pe twin:
  parse the emitted thin+fat outputs, assert section<->phdr agreement,
  entry inside .text, a sane import directory, and the header fitting
  the [0x80,0x7FE] budget. testdata/ape/apetest/pe_test.go is today
  the stub's regression suite: post-removal it asserts the stub shape,
  including TestPETextSectionInsideStubHeader (Sections[0].Offset <
  elfOffset 0x10000, i.e. .text raw data inside the APE head) and
  TestPEEntrypointIsPlaceholderStub (entry bytes 31 C0 C3). Those two
  flip back to the embedded-payload shape this wave: .text raw data at
  or after 0x10000 inside the mapped amd64 image, and an entry that is
  _rt0_cosmo_nt rather than xor/ret. The rest (MZ, e_lfanew 0x80,
  PE32+, console subsystem, alignments, .text RX-not-W, debug/pe
  parseability) carries over with tightened expected values
  (Characteristics 0x223, DllCharacteristics 0x8100, ImageBase
  0x100000000).

## Runtime NT personality design

- Host detection: the PE entry only ever runs on Windows, so the stub
  sets runtime.__hostos = _HOSTWINDOWS (=2, already declared:
  os_cosmo_amd64.go:15, var linkname :27, GLOBL
  rt0_cosmo_amd64.s:22-23) before joining the common boot - the same
  trick real cosmo uses ("you KNOW you're on NT because you entered
  via WinMain", winmain.greg.c:312-316). Dispatch idiom: iswindows()
  + a CHECK_WINDOWS asm macro, twins of the darwin isdarwin()
  (os_cosmo_amd64.go:32-34) / CHECK_DARWIN (sys_cosmo_amd64.s:85-88,
  asm_cosmo_amd64.s:45-48) pattern. The landmine being closed:
  CHECK_DARWIN falls through to the raw Linux SYSCALL path for any
  non-XNU host, so __hostos=2 today would execute raw syscalls -
  undefined behavior on NT at the first one. Rule: no raw SYSCALL may
  execute when hostos==NT. The Syscall6 dispatcher
  (asm_cosmo_amd64.s:73) gets a CHECK_WINDOWS -> ENOSYS safety net,
  and the syscall package grows a WindowsFns hook table mirroring
  DarwinFns (syscall_cosmo_arm64.go:28-87, SetDarwinFns :98-101),
  wave-1 populated with Write/Exit only - that covers fmt output and
  os.Exit, since user-level syscall.Write funnels through
  cosmo.Syscall6 (syscall_cosmo.go:39-64).
- Boot stub _rt0_cosmo_nt (new asm): cld; x87 fldcw re-init per the
  APE spec's Windows section (ape/specification.md:516-546 - NT
  initializes the FPU to 64-bit precision); capture the two IAT slots
  into runtime globals; set __hostos=2; fabricate a SysV boot stack ON
  the OS stack: argc=1, argv={ptr to static "APE\0", NULL},
  envp={NULL}, auxv={AT_PAGESZ=0x1000, AT_RANDOM -> 16 bytes seeded
  from RDTSC mixing (the ProcessPrng upgrade is wave 2), AT_NULL} -
  sysargs walks exactly this layout (os_cosmo.go:170-221, sysauxv
  :225-240). The /proc/self/auxv and mmap+mincore fallbacks
  (os_cosmo.go:189-210) are Linux-syscall paths and must not be
  reached; malloc.go:419 throws if physPageSize==0, so the AT_PAGESZ
  fabrication is mandatory. Then JMP _rt0_amd64 (asm_amd64.s:15-18).
  Real GetCommandLineW argv/env parsing is wave 2 (goargs/goenvs NT
  branches; goenvs os_cosmo.go:256-258).
- TLS: switch cosmo/amd64 from FS:-8 to a 1-insn GS:0x28 model - g
  lives at gs:0x28, which on NT is the TEB ArbitraryUserPointer field
  (upstream Go windows/amd64 precedent) and needs NO setup syscall,
  and on Linux is reachable by arch_prctl(ARCH_SET_GS=0x1001,
  &m.tls[0] - 0x28). Changes: the Hcosmo TLS lowering segment prefix
  0x64 -> 0x65 (asm6.go:2544-2554), ld Tlsoffset -8 -> +0x28
  (sym.go:73-93: Hcosmo leaves the ELF -1*PtrSize group at :81-93),
  settls's linux path ARCH_SET_FS(0x1002) -> ARCH_SET_GS with the
  -0x28 bias (sys_cosmo_amd64.s:807-823; its ADDQ $8 compensation at
  :810 dies), and clone drops CLONE_SETTLS (:735-737 - the kernel can
  only set FS) with the child calling settls itself (child block
  :748-771). All ~25 g-reload sites go through the REG_TLS
  pseudo-register (go_tls.h) and re-lower automatically: sigtramp
  sys_cosmo_amd64.s:537, the clone child :763, the asm_amd64.s set
  (rt0_go, gogo, mcall, systemstack, morestack, asmcgocall,
  cgocallback, setg, stackcheck), preempt_amd64.s:34; the only
  hard-coded -8 assumptions in cosmo asm are the two compensations
  named above. The rt0_go TLS store-through test (asm_amd64.s:286-291)
  validates the model on the Linux leg immediately. darwin-amd64 is
  unaffected (its runtime is incomplete and its settls branch is
  already a no-op RET, :819-823; its future path is pthread-key
  fishing until a key lands at slot 0x28).
- NT function table (new os_cosmo_nt.go): resolved at osArchInit when
  hostos==NT via the captured GetProcAddress/LoadLibraryA -
  os_cosmo_amd64.go:39 is a no-op today and becomes the resolution
  hook, mirroring the arm64 darwin resolve at os_cosmo_arm64.go:257.
  Wave-1 set: kernel32 VirtualAlloc/VirtualFree/WriteFile/
  GetStdHandle/ExitProcess/ExitThread/CreateThread/Sleep/
  GetSystemInfo; api-ms-win-core-synch-l1-2-0.dll WaitOnAddress/
  WakeByAddressSingle for Go-level futexsleep/futexwakeup branches
  (os_cosmo.go:37-46/:51-62). cosmo/amd64 is pure lock_futex
  (lock_futex.go:5 build tag) and every futexwakeup caller passes
  cnt=1 (lock_futex.go notewakeup/semawakeup plus
  os_cosmo_arm64_sema.go:313 - exhaustive grep), so Single suffices;
  real cosmo imports the same DLL (cosmo_futex.c). sysmon parks via
  notetsleep -> futexsleep, so this branch IS exercised by a quiet
  fizzbuzz run. Calls go through a Win64-ABI thunk in asm mirroring
  the darwin cosmoLibcCall6 shape (sys_cosmo_arm64.s:127): shadow
  space + 16-byte stack alignment.
- Clocks: no imports needed - port the in-tree upstream
  KUSER_SHARED_DATA readers (src/runtime/time_windows_amd64.s +
  time_windows.h:13-14): nanotime1/walltime CHECK_WINDOWS branches
  read _INTERRUPT_TIME 0x7ffe0008 / _SYSTEM_TIME 0x7ffe0014. (Real
  cosmo uses QueryUnbiasedInterruptTimePrecise,
  clock_gettime-nt.c:39-115; the KUSER page is upstream Go's approach
  and needs zero resolution machinery - chosen for wave 1.)
- Memory: mem_cosmo.go host branches - sysReserve ->
  VirtualAlloc(MEM_RESERVE), sysMap+sysUsed -> MEM_COMMIT, sysUnused
  -> MEM_DECOMMIT, sysFree -> VirtualFree(base, 0, MEM_RELEASE). NT
  allocation granularity is 64 KiB and partial unmap is impossible
  (spec.md:671-679: Windows "lack[s] the ability to carve or punch
  holes in mappings"; cosmo's munmap returns ENOTSUP unless the range
  envelops whole mappings, mmap.c:534-568), so sysReserveAligned's
  partial-munmap trim (the default branch, mem.go:225-245, keyed
  compile-time on `case GOOS == "windows"` at mem.go:207 today)
  becomes runtime-host-keyed and uses the windows-style
  free-and-retry loop (mem.go:207-224) when hostos==NT. physPageSize
  = 4096 via the fabricated AT_PAGESZ.
- Threads: newosproc (os_cosmo.go:109-135) NT branch -> CreateThread
  with a 64 KiB stack; the child stub pivots SP onto the
  malg-allocated g0 stack (preserving the Linux bookkeeping: mexit
  frees it, and the OS stack dies with ExitThread - the same pivot
  real cosmo does: CloneWindows CreateThread(0, 65536, ...) then
  WinThreadEntry's __stack_call rsp pivot, clone.c:76-133), writes g0
  into gs:0x28, and calls mstart. TEB StackBase/StackLimit are left
  stale for wave 1 (cosmo precedent - it does not edit the TEB
  either; VEH-only signal handling makes that safe, noted as wave-2+
  work). This is REQUIRED for the north star: runtime.main starts
  sysmon via newm before user main runs (proc.go:173-175), so thread
  creation cannot be deferred out of wave 1.
- Misc wave-1 branches: write1 (sys_cosmo_amd64.s:173) ->
  WriteFile(GetStdHandle(-11/-12)) for fds 1/2; exit (:90) ->
  ExitProcess(code) (NOTE divergence: real cosmo encodes a unix wait
  status as code<<8 for its own process protocol, exit.c; we use
  plain codes until an os/exec wave needs otherwise); exitThread
  (:104) -> ExitThread; usleep/osyield (:241/:825) -> Sleep(ms)/
  Sleep(0); getCPUCount (os_cosmo.go:64-93) -> GetSystemInfo
  dwNumberOfProcessors; readRandom (os_cosmo.go:249-254) -> return 0
  for wave 1 (AT_RANDOM covers the boot seeds via startupRand;
  ProcessPrng is wave 2); signal no-op set mirroring darwin's
  return-0 stubs (rt_sigaction's darwin leg returns 0,
  sys_cosmo_amd64.s:513-518): sysSigaction/sigprocmask/sigaltstack
  return 0 on NT (sysSigaction os_cosmo.go:405-419 throws on nonzero;
  rtsigprocmask :469 and sigaltstack :787 crash on failure, so the NT
  branches must come before the crash checks), tgkill/raise gated
  (signalM os_cosmo.go:440-446; raise/raiseproc sys_cosmo_amd64.s:
  294/:318). Netpoll is lazily initialized (netpollGenericInit
  netpoll.go:226) and untouched by fizzbuzz - explicitly deferred.
- fd model: fds 1 and 2 map straight to GetStdHandle; no fd table in
  wave 1 (open() and friends are later waves).

## CI and verification ladder

windows-latest test-job coverage returns via this branch, climbing
four rungs:

- L0: debug/pe structural asserts on the built thin+fat outputs (runs
  on every OS, including ubuntu).
- L1: stub-proof - the windows runner executes the fat APE and gets a
  deliberate constant exit code from the NT stub before full boot is
  wired (proves the loader maps our sections and runs our code).
- L2: println-level output through the runtime write path.
- L3: full fizzbuzz via fmt prints + exit 0 = the wave north star.

Land the largest honestly-verified rung; never claim past what CI
proved. The linux/macos legs stay green throughout.

## Deferred to later waves (explicit non-goals for wave 1)

Real argv/env (a GetDosArgv-equivalent with cmd.exe quoting), console
CP_UTF8/VT setup, VEH -> sigpanic/signal semantics, os/exec,
sockets/netpoll (likely a wepoll-or-IOCP design decision), async
preemption (SuspendThread/GetThreadContext), profiling timers,
os.Executable (GetModuleFileNameW), TIB stack-bounds hygiene,
ProcessPrng entropy, exit-status protocol alignment with cosmo, and
Windows/arm64.

## wave 1 implementation log (2026-07-18, step 1: header + NT stub, L1)

Landed on claude/nt-wave1:

- runtime/rt0_cosmo_nt_amd64.s (cosmo && amd64 only): `_rt0_cosmo_nt`
  entry stub (cld; fldcw 0x37f per spec.md's Windows x87 section; SUBQ
  $40,SP for win64 alignment+shadow space; LoadLibraryA("kernel32.dll")
  -> GetProcAddress(h,"ExitProcess") -> ExitProcess(42); INT3 fallback)
  plus the import machinery as fixed-layout data symbols:
  `runtime.ntidata` (0x70 bytes: IDT entry + terminator, 2-entry ILT,
  hint/name entries, "kernel32.dll") and `runtime.ntiat` (3 u64 slots,
  first two DATA-initialized to 1 so the symbol is file-backed
  noptrdata the loader can overwrite in place).
- cmd/link: `apePrepareNTBoot` (thin amd64 path, convertToAPE) resolves
  the three symbols via ctxt.loader (they are new deadcode roots for
  Hcosmo/amd64 - nothing references them by relocation), verifies the
  blob strings, and patches the five RVA fields into the payload bytes
  before writeAPEFile re-emits them. `writePECosmoAMD64` replaces the
  amd64 stub header: PE32+ at ImageBase 0x100000000, Characteristics
  0x0223, DllCharacteristics 0x8100 (no ASLR bits), subsystem CUI 6.0,
  stack 8MiB/64KiB, heap 1MiB/4KiB, SizeOfHeaders 0x400,
  NumberOfRvaAndSizes 16 with only DataDirectory[1] = {RVA(ntidata),
  0x28}; sections .text/.rodata/.data from the payload's three PT_LOADs
  (asserting vaddr - p_offset == ImageBase per load, so RVA ==
  payload-relative offset), the payload ELF-header page skipped (.text
  RVA 0x1000, raw 0x11000), .data VirtualSize=memsz > SizeOfRawData
  (=filesz rounded to FileAlignment over verified zero padding) for
  BSS. Fat path: payloadFromAPEOrELF keeps each input's 64K head and
  `transplantPEHeader` copies the amd64 input's [0x80,0x800) verbatim
  (asserting the PE sig and payload offset 0x10000); readobj output of
  thin and fat is byte-identical. arm64-only and loaderless synthetic
  payloads keep the legacy stub header.
- Tests: apetest/pe_test.go flipped from stub-shape to real-mapping
  acceptance (sections raw >= 0x10000, entry inside .text and matching
  the stub prologue fc d9 2d ?? ?? ?? ?? 48 83 ec 28, exact
  ImageBase/Characteristics/DllCharacteristics/SizeOfHeaders, kernel32
  imports = GetProcAddress+LoadLibraryA, .data BSS) while keeping the
  polyglot/stub-era invariants that still hold. ld/ape_test.go gained
  TestPECosmoHeaderStructure (synthetic payload with a pre-patched
  blob through writeAPEFile, parsed with debug/pe incl.
  ImportedSymbols) and TestAPEFatPETransplant (thin -> re-ingest ->
  fat merge, header region byte-compared and re-verified).
- CI: new `test-windows` job (windows-latest, needs build) downloads
  the ubuntu-origin and windows-origin fat fizzbuzz artifacts and
  asserts each exits 42 in pwsh on a throwaway copy ("NT stub
  exit-code check (interim wave-1 L1)"). apetest still skips execution
  on windows. ubuntu/macos legs unchanged.

L1 expectation: windows-latest runs the fat APE and gets exit code 42
- that is the deliberate, temporary contract until L2 wires the write
path (then the stub stops exiting early and the check moves up the
ladder).

Verified locally (linux): make.bash; thin+fat fizzbuzz builds; both
run correctly on linux via throwaway copies (self-assimilation
intact); llvm-readobj on pristine copies shows the designed
header/sections/imports, thin==fat; apetest green against fat (thin
fails only the three inherently-fat TestFat* checks, as before);
cmd/link/internal/ld suite green.

Deviations from the design/step brief, and why:

- The ntidata patch is applied to the payload bytes in memory before
  writeAPEFile re-creates the output, not by seek+write on the
  finished file: convertToAPE's existing idiom is read-whole-file ->
  rebuild, so mutating p.elf is the same mechanism with less code.
  Byte-level outcome is identical (verified in the output file).
- .text/.rodata keep exact (unaligned) SizeOfRawData; only .data is
  rounded up to FileAlignment, per the step brief. Loaders accept
  unaligned raw sizes (they read whole 512-byte sectors regardless);
  revisit only if a real Windows loader complains.
- SizeOfCode/SizeOfInitializedData/SizeOfUninitializedData are 0 like
  real cosmo APEs (loaders ignore them); the design did not specify.
- The linker cross-checks the blob's embedded strings before patching
  (not in the brief) so asm/linker layout drift fails the link loudly.
- cosmo-ci.yml has no all-builds aggregator job to wire the new job
  into (the org-side required-builds-manager aggregates externally);
  publish keeps needs [build, test], consistent with wasm not gating
  publish either.

## step 2: NT personality (runtime, inert; TLS switch live)

Landed in two commits on claude/nt-wave1. The entry stub still exits
42 (untouched); everything below is dead code until a later step flips
the stub to set __hostos=2 and join the common boot.

Phase A - TLS switch fs:-8 -> gs:0x28, LIVE on all hosts (the one
non-gated change; cosmo/amd64 codegen-global):

- asm6.go Hcosmo REG_TLS prefix 0x64->0x65; ld sym.go Hcosmo Tlsoffset
  -8 -> +0x28 (arm64 unaffected: R_ARM64_TLS_LE ignores Tlsoffset);
  settls linux leg ARCH_SET_FS(+8) -> ARCH_SET_GS(base=&m.tls[0]-0x28);
  clone drops CLONE_SETTLS (kernel can only set FS), child runs the
  arch_prctl itself before its first g access. All other g reloads
  lower through REG_TLS and re-lowered automatically; a tree grep found
  no other hard-coded FS/-8 assumptions.
- Validation evidence: fat runtimeprobe on linux prints the full
  gauntlet through "ok segvrecover / ok sigterm / ok sigusr2 /
  ok preempt / ok execchild / ok waitsig killed / ok all", exit 0 -
  sigtramp's get_tls reload, async preemption, clone'd threads and
  os/exec children all agree on the new model. sync + sync/atomic via
  the misc/cosmo wrappers pass; cmd/link/internal/ld (APE+PE suite)
  passes; thin/fat fizzbuzz output identical.
- Debug war story: the first post-switch build segfaulted at absolute
  0x28 with GS correctly set. Core + raw objdump showed ~11 early-text
  sites (internal/cpu's post-ABI0-call g reloads) encoded 64 (FS
  prefix, old object) with displacement 0x28 (new linker) - the fork
  reports a RELEASE version string, so cmd/go's tool build IDs are
  version-based and a rebuilt compiler does NOT invalidate cached
  GOOS=cosmo std objects. Rule for toolchain hackers: go clean -cache
  (or -a) after every make.bash before trusting a cosmo binary.
  [FIXED 2026-07-20: this same defect, amplified through go-toolchain
  CI's persistent shared GOCACHEPROG web cache, shipped consumer APEs
  that SIGSEGV'd at startup in internal/cpu.doinit (fault addr 0x28 -
  the identical crash class). Fork tools now print their own build ID
  under -V=full and cmd/go derives tool IDs from tool content, so a
  rebuilt toolchain invalidates stale cache entries automatically; the
  clean-cache rule is obsolete. See CLAUDE.md Fork Gotchas.]

Phase B - NT personality, all gated on __hostos==_HOSTWINDOWS (=2,
still never set):

- Dispatch prims: iswindows() (os_cosmo_amd64.go; constant-false twin
  in os_cosmo_nt_arm64.go so shared code compiles away) and a
  CHECK_WINDOWS asm macro beside each file's CHECK_DARWIN, always
  checked before the Linux fallthrough.
- Foreign calls: runtime·ntcall6 (sys_cosmo_nt_amd64.s) is the
  SysV->win64 trampoline; Go packs struct{fn,a1..a6,ret} on the stack
  and asmcgocall(tramp,&args) provides the g0 switch; the tramp keeps
  the args pointer in DI (win64 callee-saved), SUBQ $56 for shadow
  space + args 5/6 + 16-alignment, RAX -> ret. Thin wrapper
  ntcall(fn,a1..a6); nosplit + noescape => usable pre-mallocinit.
  GetLastError helper deliberately omitted (nothing consumes errors in
  wave 1).
- ntlib: resolved at osArchInit (osinit already runs it before
  getCPUCount) from the two loader-filled IAT slots; kernel32:
  VirtualAlloc/VirtualFree/WriteFile/GetStdHandle/ExitProcess/
  ExitThread/CreateThread/Sleep/GetSystemInfo, synch forwarder DLL:
  WaitOnAddress/WakeByAddressSingle; ntStdout/ntStderr cached.
  Resolution failures crash-poke 0xf2..0xf5 (registry in
  os_cosmo_nt.go). Deviation from the design sketch: plain
  runtime·nt*Fn vars instead of a struct, so asm references them by
  name with zero offset-rot risk (cosmoPthread*Fn precedent).
- Insertion points (sys_cosmo_amd64.s unless noted): exit->
  ExitProcess, exitThread->ExitThread (direct win64 calls, process/
  thread dying); write1 -> ntwrite1tramp -> Go ntwrite1 (fds 1/2 ->
  cached handles, else -EBADF; WriteFile via ntcall); usleep ->
  Sleep(ceil ms), osyield -> Sleep(0) (direct calls, SUBQ $40
  discipline); nanotime1/walltime -> KUSER_SHARED_DATA readers ported
  from upstream time_windows_amd64.s (single atomic 64-bit lo|hi1
  load; walltime rebases the 1601 epoch by 116444736000000000);
  futexsleep/futexwakeup (os_cosmo.go) -> WaitOnAddress (ms rounded
  up, INFINITE for ns<0, val copied to stack) / WakeByAddressSingle
  (cnt is provably always 1); futex asm keeps an ENOSYS net.
- Memory (mem_cosmo.go): sysAllocOS -> VirtualAlloc(RESERVE|COMMIT)
  [addition to the brief's list - it is on the mallocinit path],
  sysReserveOS -> MEM_RESERVE with hint-retry-at-NULL, sysMapOS +
  sysUsedOS -> MEM_COMMIT (throw on failure), sysUnusedOS ->
  MEM_DECOMMIT, sysFreeOS -> VirtualFree(base,0,MEM_RELEASE) (0xf6
  poke on failure), sysFaultOS -> decommit, sysNoHugePageOS no-op;
  sysReserveAligned's partial-unmap trim keys at runtime via
  cosmoHostIsWindows() (false stub in stubs_noncosmo.go) and takes the
  windows release-and-retry loop on NT.
- Threads: newosproc -> CreateThread(64KiB reservation stack) ->
  tstart_cosmo_nt (win64 entry, CX=mp): pivots onto mp.g0's
  Go-allocated stack, stores g0 to gs:0x28 (plain TEB store, no
  settls), mirrors the clone-child wiring, mstart. TEB StackBase/
  Limit left stale per design; thread handle leaked in wave 1;
  m.procid stays 0 (minitProcid gated - gettid is a raw syscall and
  signal sends are dropped anyway).
- Signals inert: rt_sigaction/rtsigprocmask/sigaltstack asm return
  0/no-op before their crash checks (darwin return-0 stub idiom);
  raise/raiseproc/tgkill asm no-op; signalM and
  setProcessCPUProfiler gated in Go; sbrk0 returns 0 (straggler
  found by the boot-path grep: mallocinit queries the brk).
- Boot guards: sysargs early-returns before the /proc/self/auxv and
  mmap+mincore fallbacks; readRandom returns 0 (AT_RANDOM covers boot;
  wave 2: ProcessPrng); getCPUCount -> GetSystemInfo
  dwNumberOfProcessors (offset 32).
- Syscall safety nets: cosmo.Syscall6 asm returns ENOSYS on NT before
  any SYSCALL; src/syscall's rawSyscallNoError/rawVforkSyscall
  crash-poke 0xf7/0xf8 (they cannot report errors); RawSyscall6
  routes through a WindowsFns hook table (Write/Exit only, mirroring
  SetDarwinFns; installed by the runtime at osArchInit) so fmt output
  and os.Exit work, everything else ENOSYS.

Validation (linux): make.bash; go clean -cache; std builds for
cosmo/amd64 AND cosmo/arm64 (plus windows/amd64 + darwin/arm64
runtime compiles - mem.go and stubs_noncosmo.go are GOOS-generic);
vet clean for runtime/syscall/cosmo pkg on both cosmo arches; the
whole Phase-A battery repeated green (runtimeprobe "ok all" exit 0,
sync tests, ld tests, thin+fat fizzbuzz).

## step 3: boot flip - fizzbuzz runs on Windows (L3 north star)

Landed in two commits on claude/nt-wave1 (runtime boot cfaa3be1, CI
flip 977759d7). The stub's ExitProcess(42) tail is gone; the fat APE
boots the real cosmo runtime on NT and fizzbuzz prints.

What flipped:

- _rt0_cosmo_nt (rt0_cosmo_nt_amd64.s): keeps cld + fldcw 0x37f, then
  stores __hostos = 2 directly (the WinMain trick - reaching the PE
  entry point proves the host; the unix rt0 gets CL from the APE
  loader instead), fills runtime.ntbootrand (16-byte file-backed
  noptrdata) with two RDTSC reads mixed with SP and the loader's CX
  (PEB) - wave 2 upgrades to ProcessPrng - and builds the SysV boot
  block on the OS stack: [argc=1]["APE" argv][NULL][NULL envp]
  [AT_PAGESZ 0x1000][AT_RANDOM &ntbootrand][AT_NULL], 16-aligned,
  argc at 0(SP), then JMP _rt0_amd64. The IAT needs no capture step
  (osArchInit reads runtime.ntiat directly). The old
  LoadLibraryA/GetProcAddress/ExitProcess tail and its string blobs
  are deleted.
- goargs NT branch (runtime1.go hook -> cosmoNTGoargs,
  os_cosmo_nt.go): argslice comes from GetCommandLineW parsed per the
  Windows quoting rules - a port of os/exec_windows.go
  commandLineToArgv (provenance in comments; under GOOS=cosmo package
  os takes runtime_args(), so the parse must live in the runtime).
  Falls back to the fabricated "APE" argv on an empty command line.
  Verified ordering: schedinit runs mallocinit before goargs/goenvs
  (proc.go), so allocation there is safe; no lazy path needed.
- goenvs NT branch (ntGoenvs): GetEnvironmentStringsW ->
  double-NUL-terminated UTF-16 walk -> envs (upstream os_windows.go
  goenvs shape); FreeEnvironmentStringsW deliberately skipped (not in
  the resolve set; one-shot boot leak). GODEBUG therefore works via
  parsedebugvars. Shared decoder ntUTF16ToString handles surrogate
  pairs (runtime's gostringw does not). GetCommandLineW +
  GetEnvironmentStringsW joined the osArchInit kernel32 resolve set.
- Stub-flip fallout fixed in the same commit (all three found locally
  by running the fat APE under WINE before ever pushing - the wine
  loader approximates NT closely enough to surface real boot bugs):
  1. rt0_go's TLS store-through test (asm_amd64.s) compares the value
     written through gs:0x28 with m0.tls[0]; on NT gs:0x28 IS the TEB
     ArbitraryUserPointer storage, no alias, so the test aborted.
     GOOS_cosmo-guarded fix: when __hostos==2, read back through the
     segment reference instead. Other GOOS/hosts untouched.
  2. checkfds (fds_unix.go) throws on any fcntl errno except EBADF;
     the step-2 ENOSYS net returned 38 -> "fatal error: cannot open
     standard fds" (first wine run died here, with a PERFECT panic
     report - write path, traceback, exit all already working).
     Runtime fcntl (fcntl_cosmo_amd64.go) now has an NT branch:
     F_GETFD on fds 0-2 reports open (no fd table in wave 1),
     everything else stays ENOSYS.
  3. "stack split at bad time" at (*WindowsFns).Syscall6: the hook
     chain runs between entersyscall/exitsyscall (second wine run
     died in os.init -> os.Getwd -> syscall.Syscall(SYS_GETCWD)).
     The dispatcher and the runtime-side ntSyscallWrite/ntSyscallExit
     are now //go:nosplit, like the raw SYSCALL they replace.

Wine evidence (wine 9.0, WINEDEBUG=-all, fat APE): "10 5" ->
"fizzbuzz" rc=0; "7 6" -> "13" rc=0; "12345 6789" -> "fizz" rc=0;
"-10 -5" -> "fizzbuzz" rc=0; no args -> rc=1 with "Usage:
Z:\...\wine-fizzbuzz.com <num1> <num2>" on stderr; "abc 5" ->
"Invalid first argument: abc" rc=1. Full contract green locally -
wine is NOT conclusive for real Windows (its loader and kernel32 are
reimplementations), CI is the truth, but it made the fix loop local
and fast.

CI flip (cosmo-ci.yml test-windows): checkout + setup-go joined the
job; the exit-42 step became "Run fizzbuzz on Windows (NT boot
acceptance)" - for ubuntu-origin and windows-origin fat artifacts it
runs the apetest contract cases 10+5 -> "fizzbuzz\n" and 7+6 ->
"13\n" (the second proves argv flows through GetCommandLineW parsing
rather than printing a constant), byte-compares stdout captured via
-RedirectStandardOutput, prints "origin: exit code N", and keeps the
Start-Process + explicit-exit hardening. Then the full apetest suite
runs natively against both origins (fizzbuzz_test.go's windows skip
replaced by direct CreateProcess invocation; with-deadline.sh already
degrades to plain exec on git-bash, so the watchdogs there are the
in-test 3-minute contexts plus step timeout-minutes; RUNTIMEPROBE_BIN
deliberately unset - the probe needs later-wave NT surface, its
execution test self-skips). apetest's TestPEEntrypointIsNTStub
keyhole now asserts the new prologue: fc (cld), d9 2d (fldcw), c7 05
(movl imm32 to rip-relative m32) with immediate 02 00 00 00
(_HOSTWINDOWS) - byte positions verified against the built binary.

Local validation (linux, after the final iteration): fresh make.bash
+ go clean -cache; thin+fat fizzbuzz correct on throwaway copies; fat
runtimeprobe "ok all" exit 0; full apetest suite green vs fat
(FIZZBUZZ_BIN+RUNTIMEPROBE_BIN, upstream go); cmd/link/internal/ld
suite green; std builds for cosmo/amd64 + cosmo/arm64 +
linux/amd64, runtime compiles for windows/amd64 + darwin/arm64, vet
clean for runtime and internal/runtime/syscall/cosmo.

CI evidence (run 29638954834, first push of the flip - green on the
FIRST windows round, all 8 jobs success): the windows-latest
acceptance step printed, verbatim,

	ubuntu-latest: launching D:\a\_temp\fizzbuzz-ubuntu-latest.com 10 5
	ubuntu-latest: exit code 0 (want 0), stdout "fizzbuzz\n"
	ubuntu-latest: launching D:\a\_temp\fizzbuzz-ubuntu-latest.com 7 6
	ubuntu-latest: exit code 0 (want 0), stdout "13\n"
	windows-latest: launching D:\a\_temp\fizzbuzz-windows-latest.com 10 5
	windows-latest: exit code 0 (want 0), stdout "fizzbuzz\n"
	windows-latest: launching D:\a\_temp\fizzbuzz-windows-latest.com 7 6
	windows-latest: exit code 0 (want 0), stdout "13\n"

and both native apetest runs ended "ok apetest" (246 PASS lines
across the two origins, every fizzbuzz execution test including the
TestError_* exit-1/stderr cases passing on real Windows;
TestRuntimeProbe SKIP as designed). The linux/macos test legs, wasm,
and publish all stayed green. The wave-1 north star - a normal fat
APE printing real fizzbuzz output and exiting 0 on windows-latest -
is CI-proven; no debug-exit-code probes were ever needed on the
runner (the wine prepass caught all three boot bugs locally).

Deviations from the step brief: none of substance. The brief allowed
lazy argv parsing if goargs ran pre-malloc - not needed (ordering
verified). wine was listed as optional/inconclusive - it turned out
decisive for iteration speed, converting what would have been 3 CI
round-trips into local minutes. The direct-invocation CI check runs
two success cases (both origins); the error-path cases are covered by
the native apetest leg rather than duplicated in pwsh.

### step-3 postscript: the ASLR straddle flake (fixed same day)

The docs-only push a94c1891 (identical code to the green 977759d7 run)
FAILED test-windows: windows-origin apetest, TestFizzbuzz_Negative15,
exit status 0xC0000005 with captured stderr

	fatal error: runtime: cannot commit pages
	runtime.sysUsedOS(0x217d7bffc000, ...)
	runtime.(*mheap).allocSpan -> stackpoolalloc -> stackalloc(0x4000)
	runtime.schedinit

while the very next push's run (4175ebce) was green again - a
nondeterministic 1-in-~92-boots crash. Forensics: the failing range
ends exactly at 0x217d7c000000, a 64MiB heap-arena boundary. NT only
lets one VirtualAlloc(MEM_COMMIT)/VirtualFree(MEM_DECOMMIT) call touch
pages of a SINGLE prior reservation; adjacent 64MiB arena reservations
merge virtually in the heap's eyes, so the first span allocated
straddling the boundary (fresh pages are born scavenged, so allocSpan
sysUsed-commits them) issues one commit across two reservations ->
ERROR_INVALID_ADDRESS -> throw. Whether arenas land adjacent depends
on NT address-space randomization, hence the flake; wine's different
layout never produced it. (The 0xC0000005 exit instead of exit(2) is
the panic path faulting while unwinding past the wedged heap state -
secondary, unreachable once the commit works.) Fix (same one upstream
mem_windows.go has carried for years, ported verbatim as
ntCommitPages/ntDecommitPages in mem_cosmo.go): on failure retry
successively smaller page-aligned chunks so each call stays within one
reservation; sysUsedOS, sysUnusedOS, sysFaultOS, and sysMapOS NT
branches all route through the chunked helpers now. Locally green
after the fix: linux fizzbuzz+probe ("ok all"), apetest, wine
fizzbuzz, cosmo/arm64 std, vet.

Post-fix verification: the straddle-fix push (2f0d0f34, run
29639718819) and an additional workflow_dispatch sample on the same
SHA (run 29639992692) were both fully green - two consecutive
windows-latest rounds, roughly 184 fresh-ASLR process boots, zero
failures - on top of the boundary-address forensics and the fix's
upstream mem_windows.go provenance. Branch CI history for step 3:
977759d7 green (first flip push), a94c1891 FAIL (the straddle flake,
identical code), 4175ebce green, 2f0d0f34 green, dispatch green.

## 2026-07-18: fat APEs strip by default; debug sidecars (.dbg / .aarch64.elf)

Default `GOOS=cosmo` fat builds now ship stripped, with full debug info
in per-arch sidecar ELFs next to the output - the cosmocc convention,
copied exactly (cosmocc's apelink embeds only each input's PT_LOAD file
span and moves the unstripped per-arch linker outputs to `<out>.dbg` /
`<out>.aarch64.elf`; no objcopy, no .gnu_debuglink anywhere in
Cosmopolitan).

**Mechanism**: cmd/go's fat merge (cosmoFatten / cosmoFattenInstall)
passes two new linker flags to `-apefat`: `-apedbg` first writes each
payload's pristine ELF image - exactly the bytes the per-arch link
produced, p_offsets normalized, symtab+DWARF+section table intact - to
`<output>.dbg` (cosmo amd64) and `<output>.aarch64.elf` (cosmo arm64);
`-apestrip` then embeds only each payload's loadable span (max over
program headers of p_offset+p_filesz) with e_shoff/e_shnum/e_shstrndx
zeroed. Safe because every APE boot path is phdr-only: the embedded
printf boot headers already zero the section fields, self-assimilation
rewrites just the 64-byte ehdr, the Mach-O header derives from
PT_LOADs, and ape-m1.c never reads e_shoff (verified: it only declares
the field). Opt-outs: GOCOSMOSTRIP=0 (parsed like GOCOSMOFAT) restores
full embedded payloads with no sidecars, byte-for-byte; an explicit
`-s`/`-w` in the user's -ldflags means the merge adds no flags at all
(user intent wins - payloads are their stripped output, no sidecars).
`go test` / `go run` / thin GOCOSMOFAT=0 builds never reach the merge
and are untouched.

**How to debug a stripped APE**:

- `gdb program.com.dbg` - the amd64 sidecar is a complete, runnable
  (on Linux) ELF with full symtab and DWARF; debugging it IS debugging
  the same code the APE runs. Same for `program.com.aarch64.elf` on
  ARM64 hosts. First run `set osabi GNU/Linux` inside gdb: the APE
  spec mandates ELFOSABI_FREEBSD and stock Ubuntu gdb has no FreeBSD
  handler, so without the override `run` dies in gdb's unwinder
  ("Architecture rejected target-supplied description" then
  frame_unwind internal-error; verified gdb 15.1). With it,
  break/run/bt with source lines all work (verified).
- Against the shipped binary itself: run a copy once so it
  self-assimilates to ELF, then
  `gdb assim.com -ex 'set osabi GNU/Linux' -ex 'symbol-file program.com.dbg'`
  - the sidecar is ET_EXEC at the payload's own vaddrs, so symbols
  land without any address argument (verified: breakpoints hit and
  backtraces resolve inside the running stripped binary).
- `go tool nm` / `objdump` / `addr2line` / delve / `go version`: point
  them at the sidecar, not the APE. (`go version` has never been able
  to read a fat APE - the MZ magic makes it sniff as a PE with no Go
  build info; verified identical on pre-strip GOCOSMOSTRIP=0 output -
  but it reports fine against the `.dbg` sidecar.)
- Runtime tracebacks, panics, and runtime/pprof are UNAFFECTED by
  stripping: Go symbolizes through gopclntab, which lives in a loaded
  segment inside every payload. Only ELF-symtab/DWARF consumers need
  the sidecars.

**Names are load-bearing**: cosmo libc's FindDebugBinary probes
`<exe>.dbg`, `<exe>.com.dbg`, and (on aarch64) `<exe>.aarch64.elf` at
crash time, so the chosen suffixes keep cosmo-ecosystem tooling
conventions working against Go-built APEs.

**Size** (measured, this change): fizzbuzz.com 5084226 -> 3420736
bytes (-32.7%), runtimeprobe.com 7305033 -> 4971712 (-31.9%), and a
stdlib-heavy webserver (net/http, crypto/tls, image/png, time/tzdata)
17286764 -> 12269216 (-29.0%; 3.6 MB on the wire after
`zstd -19 --long=27`) versus the unstripped fat builds, whose sizes
GOCOSMOSTRIP=0 still reproduces exactly. The sidecars carry the
stripped-off bytes; nothing is lost (the .dbg sidecar is
byte-identical to the payload a GOCOSMOSTRIP=0 build embeds -
verified).

**Tests**: cmd/link apefat_test.go (sidecar byte-identity incl. the
thin-APE extraction round-trip, symtab parseability, stripped-span
layout, flags-off byte-compatibility), cmd/go cosmofat_test.go
(GOCOSMOSTRIP parsing, -ldflags -s/-w token detection), apetest
(TestELFPayloadStripped, TestFatPayloadsStripped, TestDebugSidecars -
the last skips when sidecars aren't next to FIZZBUZZ_BIN, as on CI
test runners, which download bare artifacts).

## 2026-07-19: GOCOSMODEBUG - slim sidecars and compact in-binary debug info

Two opt-in debug tiers on top of the strip-and-sidecar default. The
default (GOCOSMODEBUG unset or `full`) is byte-identical to before the
knob existed - verified by rebuilding at the base commit with the
patched toolchain and byte-comparing all six artifacts. Invalid values
fail the build. With GOCOSMOSTRIP=0 or user `-s`/`-w` there are no
sidecars at all, so the knob is a no-op. Plumbing: cmd/go parses the
env var (cosmofat.go) and passes `-apedbgmode=slim|compact` to the
`-apefat` merge; the transforms live in cmd/link/internal/ld/apedebug.go.

**slim** rewrites each sidecar into the in-linker equivalent of
`objcopy --only-keep-debug`: the header page (ehdr, phdrs, notes) is
preserved verbatim, allocated sections lose their file contents and
become SHT_NOBITS with name/addr/size/flags intact, `.symtab`,
`.strtab`, and every `.debug_*` keep their bytes (still zlib'd),
program headers are clamped to the retained span. Section indices are
unchanged, so st_shndx and sh_link stay valid. Not runnable (the
cosmocc-parity "run the .dbg directly" property is the price); every
debugger flow works unchanged. The shipped APE is byte-identical to
the default build's.

**compact** writes slim sidecars AND appends per-arch debug views past
the fat APE's last payload - outside every loadable span, never mapped,
proven inert (all APE boot paths are phdr-only; full apetest execution
passes on compact binaries). Each payload's ELF header - and the boot
header self-assimilation printf-writes over the file's first 64 bytes
(makeEmbeddedElfHeader propagates section fields exactly when they
point past the payload image, a shape only compact payloads have) -
references its view by absolute file offset. The assimilated `.com` is
then a debugger-readable ELF by itself: allocated sections point at
the real payload bytes inside the file (no duplication), the tail
carries `.symtab`/`.strtab`/`.shstrtab` plus
`.debug_info/abbrev/line/rnglists/addr/frame` (zlib'd as the linker
emitted them). Only `.debug_loclists` is dropped: variables/args print
as <optimized out> (that is sidecar territory). `.debug_frame`
(~50 KB/arch zlib'd) is kept deliberately - without CFI, a breakpoint
in a function prologue grows a bogus `?? ()` frame between it and its
caller (measured; with frame the backtrace is clean everywhere).

**Measured** (bytes, exact; fizzbuzz / runtimeprobe):

| artifact | full (default) | slim | compact |
|---|---:|---:|---:|
| fizzbuzz.com | 3,551,808 | 3,551,808 (=) | 5,019,976 (+41.3%) |
| fizzbuzz.com.dbg | 2,700,303 | 912,016 (-66.2%) | 912,016 |
| fizzbuzz.com.aarch64.elf | 2,464,731 | 815,712 (-66.9%) | 815,712 |
| fizzbuzz total | 8,716,842 | 5,279,536 (-39.4%) | 6,747,704 (-22.6%) |
| runtimeprobe.com | 5,119,168 | 5,119,168 (=) | 7,167,264 (+40.0%) |
| runtimeprobe.com.dbg | 3,893,808 | 1,278,128 (-67.2%) | 1,278,128 |
| runtimeprobe.com.aarch64.elf | 3,598,462 | 1,163,008 (-67.7%) | 1,163,008 |
| runtimeprobe total | 12,611,438 | 7,560,304 (-40.1%) | 9,608,400 (-23.8%) |

Wire (zstd -19 --long=27, runtimeprobe): the slim release set tars to
3,806,748 vs 3,807,272 for the pristine set - the diet is wire-NEUTRAL
(long-range matching already deduped the sidecars' payload
duplication); it is a disk win, ~-5 MB next to every binary. The
compact APE alone wires at 3,422,898 vs 1,763,012 default (appended
DWARF is already zlib'd; outer zstd can't reduce it much) - still
-10% vs shipping APE+sidecars. Codec: [Superseded 2026-07-20: cosmo
DWARF is now zstd - see the round-2 section below. The round-1
measurement quoted here was zstd-DEFAULT level; SpeedBestCompression
buys -13..-16%, and the owner directed the switch.] the sidecar DWARF
stayed zlib-BestSpeed in round 1; recompression was measured at only
-8..-12% of the DWARF bytes (<= 115 KB/sidecar, zstd-default or
zlib-default) - noise next to the -67% diet, and zstd would add a
gdb-13+/binutils-2.40+ floor for it (zstd DOES work in gdb 15.1 and
delve 1.27, both verified, if it is ever wanted). DWARF is already v5
on cosmo (upstream Dwarf5 GOEXPERIMENT default); there was no v4->v5
work to do.

**gdb recipes** (gdb 15.1; `set osabi GNU/Linux` must come BEFORE the
`file` command - as a positional argv the -ex runs too late and
addresses print truncated):

    # slim (or full) sidecar against the shipped binary
    gdb -q -ex 'set osabi GNU/Linux' -ex 'file assim.com' \
        -ex 'symbol-file program.com.dbg'
    # compact: the assimilated binary alone, no sidecar anywhere
    gdb -q -ex 'set osabi GNU/Linux' -ex 'file assim.com'

Both verified live: breakpoints hit, `bt` shows file:line for every
frame; slim additionally resolves args (loclists present), compact
prints <optimized out>.

**delve gotchas** (dlv 1.27): always `--check-go-version=false` (it
parses `go1.26.4cosmo` as go0.0 and refuses). `dlv exec`/`dlv attach`
work on processes launched from a runnable PRISTINE sidecar
(GOCOSMODEBUG=full). Attaching to an assimilated APE process does NOT
work with detached symbols: delve resolves /proc/pid/exe (the
stripped APE, sectionless) and silently ignores a mismatched
executable argument -> "no debug info found". Live-debug an
assimilated APE with gdb + symbol-file instead, or take a core:
`gcore` under gdb (osabi recipe above), then
`dlv core program.com.dbg core.NNN --check-go-version=false` - full
goroutine backtraces with sources from the slim sidecar (verified).

**apetest**: TestELFPayloadStripped/TestFatPayloadsStripped branch on
the payload's e_shoff - zero means the strict stripped contract
(default and slim), nonzero means the compact contract (view past the
load span, boot/stored header agreement, simulated assimilation of
both arches parses with symtab + line-level DWARF and no loclists).
A compact payload slice is deliberately NOT a self-contained ELF; the
generic ELF-shape tests parse the assimilated whole-file form instead
(payloadELF helper). TestDebugSidecars passes unchanged for all
modes (slim keeps ET_EXEC, .text entry, symtab, .debug_info).

## 2026-07-20: debug round 2 - zstd DWARF codec + GOCOSMODEBUG=min

Two changes on top of the knob above, per the owner's directive to
"just directly use zstd for debug data compression instead of zlib"
plus making the debug info itself smaller.

**zstd codec (cosmo only).** cmd/link now compresses `.debug_*`
sections for GOOS=cosmo ELF output with vendored klauspost zstd at
SpeedBestCompression (encoder concurrency pinned to 1 for determinism;
dwarfcompress already parallelizes per-section), emitting
ELFCOMPRESS_ZSTD (type 2) Chdrs - `dwarfCompressCodec` /
`newDwarfCompressor` in ld/data.go. Non-cosmo targets keep the
upstream zlib(BestSpeed) path. This changes EVERY cosmo mode's debug
bytes (full sidecars, slim/min/compact, GOCOSMOSTRIP=0 embedded
payloads): -13..-16% of stored DWARF for ~+0.2s/arch of link time.
The shipped stripped APE keeps identical loadable spans; only inert
bytes move (dead post-strip shdr size fields in the retained header
page, and the content-derived build ID) - execution and apetest are
unaffected, verified across all four modes.

**Consumer floor for the zstd sections**: gdb >= 13, binutils >= 2.40
(readelf/objcopy), Go debug/elf >= 1.21 (internal/zstd), delve (its
DWARF reader handles ELFCOMPRESS_ZSTD). Verified live here: gdb 15.1
breakpoints + file:line bt from zstd sidecars AND from the compact
in-binary view (whose tail packs the sections as emitted, so its
Chdrs are now zstd too - re-verified standalone on the assimilated
.com); binutils 2.42 readelf --debug-dump decompresses; dlv 1.27
`core` against a zstd slim sidecar gives full goroutine backtraces
with sources. Older-than-the-floor tools see "unsupported compression
type 2" - that is the deliberate cost of the directive; `full`
sidecars remain runnable pristine ELFs either way.

**GOCOSMODEBUG=min** is a fourth tier below slim: sidecar shape is
exactly slim's (cmd/go maps it to `-apedbgmode=slim`; the linker knows
only full/slim/compact), plus cmd/go's BuildInit injects two compiler
flags into every GOOS=cosmo compile (cosmoDebugInit -> forcedGcflags):
`-dwarflocationlists=false` and `-gendwarfinl=0`. Effects, measured
and verified live in gdb:

  - breakpoints and `bt` file:line stay exact (line tables intact);
  - argument/local VALUES are untrustworthy: `info args` shows
    <optimized out> or GARBAGE (without location lists the single
    fallback fbreg location is valid at almost no PC - a leaf
    function's register arg reads as stack junk that LOOKS real, e.g.
    n=140737488340574 where slim shows n=15). Treat min as
    "backtraces yes, variables no";
  - no inlined-call frame expansion in debuggers (gendwarfinl=0);
  - runtime tracebacks, runtime/pprof, and their inline unwinding are
    UNAFFECTED (pclntab, not DWARF; the full runtimeprobe battery -
    sigpanic recovery, cpuprof, preempt - is green under min).

User `-gcflags` still win (forced flags come first, last flag wins;
verified: `-gcflags='all=-dwarflocationlists=true -gendwarfinl=2'`
under min restores full-DWARF sidecar sizes). The injected flags are
part of the build-cache key, so each tier with distinct gcflags keeps
its own cached std objects - switching between min and other modes
recompiles affected packages (correct, just slower on first switch).
Invalid GOCOSMODEBUG values now stop EVERY cosmo build at init, not
just fat merges. min's compile-time trims apply wherever cosmo
compiles happen (go test binaries, GOCOSMOSTRIP=0 builds); sidecar
shaping still only happens where sidecars are written.

**Measured** (bytes; deltas vs the 848b6387 pre-round-2 baselines in
the same column; fb = fizzbuzz, rp = runtimeprobe; sidecar pair =
.dbg + .aarch64.elf):

| artifact | full | slim | min | compact |
|---|---:|---:|---:|---:|
| fb .com | 3,388,032 (=) | 3,388,032 | 3,388,032 | 4,707,384 (-168,464) |
| fb .dbg | 2,569,178 (-98,696) | 838,240 (-98,696) | 519,944 (new) | 838,240 |
| fb .aarch64.elf | 2,268,282 (-102,336) | 717,568 (-102,336) | 436,560 (new) | 717,568 |
| rp .com | 5,517,216 (=) | 5,517,216 | 5,517,216 | 7,539,064 (-264,040) |
| rp .dbg | 4,119,348 (-161,216) | 1,286,584 (-161,216) | 803,256 (new) | 1,286,584 |
| rp .aarch64.elf | 3,698,825 (-167,384) | 1,132,304 (-167,384) | 699,424 (new) | 1,132,304 |

zstd alone is -10.5..-12.9% of a slim sidecar file (-13..-16% of the
stored DWARF); min is a further -37.6..-39.2% below the zstd slim
sidecars, i.e. -44.5..-46.8% below round 1's zlib slim (rp pair:
2,747,488 zlib-slim -> 2,418,888 zstd-slim -> 1,502,680 min). rp full
totals: 13,663,989 -> 13,335,389 (zstd) -> 7,019,896 under min
(-48.6%). fizzbuzz min total 4,344,536 vs 8,426,524 baseline full
(-48.4%).

**Recipes**: identical to round 1's (gdb `set osabi GNU/Linux` before
`file`, then `symbol-file <sidecar>`; dlv `--check-go-version=false`,
core flow for assimilated processes). The only behavioral difference
per tier is the variable-fidelity ladder: full/slim resolve args, min
does not (by construction), compact resolves nothing in-binary
(loclists dropped from the view) but its slim sidecars do.

**Vendoring**: klauspost/compress v1.19.0 in src/cmd/vendor (zstd +
fse/huff0/internal deps, 69 files, ~826 KB source; Apache-2.0/BSD-3).
The fork's `*.com` gitignore rule used to swallow the vendor
`github.com/` directory component; `.gitignore` now re-includes
`src/cmd/vendor/github.com/` explicitly.

## Wave 2 — DONE (2026-07-18, chunk E green)

windows-latest CI now runs the FULL runtimeprobe gauntlet against all
three origin binaries (ubuntu-, windows-, macos-built fat APEs) and
every check passes — TestRuntimeProbe 0.41-0.44s per origin, "ok all",
exit 0, three consecutive green runs in the acceptance cycle
(https://github.com/wow-look-at-my/gosmopolitan/actions/runs/29650537288):
args/environ/mark, getpid/getppid, numcpu, monotonic, sleep/ticker/
after/ctxtimeout, tcplisten/tcpecho/deadline/tcpserver/udp,
unixsock/unixecho (real afunix.sys pathname sockets), executable,
mkdirtemp/statdir/create/readback/rename/statsize, getwd/chdir/
wdrestore, remove/rmdir, readdir/walkdir/removeall, execchild,
segvrecover, sigterm, sigusr2, preempt (180-186ms on real NT; linux
reference 159ms), waitsig. The apetest suite (fizzbuzz battery + probe
+ format tests) is green on the windows leg for all three origins.

The two real-NT-vs-wine divergences chunk E surfaced and fixed (both
invisible under wine by construction; details in the chunk E section
at the end of this file):

1. afunix.sys refuses bind with WSAEOPNOTSUPP on a socket that has
   SO_REUSEADDR set (msafd accepts the setsockopt, the bind then
   fails). net's listenStream sets SO_REUSEADDR on every stream
   listener, so every net.Listen("unix") failed. Linux treats
   SO_REUSEADDR on unix sockets as an accepted no-op, so the
   emulation now swallows it. Wine could never show this: its ws2_32
   lacks AF_UNIX entirely (socket() fails EAFNOSUPPORT — the sole
   remaining wine probe red, by wine gap, not by port gap).
2. Loopback UDP may DROP datagrams on real NT; wine's in-process
   loopback never does. The netpoller's wake channel was a
   self-connected UDP socket, so a dropped wake byte left the poller
   asleep for its full WSAPoll timeout (observed as ~5.0s stalls
   rescued at exactly a leftover 5s time.After deadline: one sigterm
   FAIL, preempt at 5.07/5.08s, 10-45s probe runs). The wake channel
   is now a connected loopback TCP pair — lossless, like upstream's
   PostQueuedCompletionStatus/pipe transports.

Wave-3 backlog (deferred, in rough priority order):

- sendmsg/recvmsg + SCM_RIGHTS-style fd passing (ENOSYS this wave;
  blocks ReadMsg*/WriteMsg*; darwin leg equally lacks it).
- SIGPROF profiling parity (setitimer-based CPU profiling; also still
  missing on the darwin leg).
- Windows/arm64 (the fat APE's arm64 image currently targets
  linux/macos hosts only).
- Real-conhost console-ctrl injection: the D2 asm handler + relay M
  are live and wine-verified via direct trampoline calls, but the
  probe does not exercise GenerateConsoleCtrlEvent (headless CI has
  no console), so the OS-injected foreign-thread path remains
  CI-unproven.
- High-resolution timers: usleep is Sleep(ms) with ~15.6ms quanta on
  real NT. Every probe timer check passes as-is; if finer sleep
  granularity is ever needed, the recipe is CreateWaitableTimerExW
  with CREATE_WAITABLE_TIMER_HIGH_RESOLUTION + per-M highResTimer
  (upstream os_windows.go:398-432).
- socketpair (ENOSYS; os/exec uses pipes).
- gofmt drift in netpoll_cosmo_xnu.go + two runtime testdata files
  (pre-wave-2 debt, deliberately not folded into NT commits).

Goal: testdata/runtimeprobe (runtimeprobe.com) green on windows-latest —
the same runtime gauntlet macOS passes: file I/O, directory listing,
pid/ppid, NumCPU, monotonic clock, timers, TCP/UDP/unix sockets, signals
(sigpanic recovery, os/signal delivery, async preemption, wait-status
decode), os/exec, os.Executable, argv/env, wd round-trip. That flips the
windows skip in apetest/runtimeprobe_test.go and sets RUNTIMEPROBE_BIN
on the test-windows apetest steps in cosmo-ci.yml.

Status: chunk A landed (identity, entropy, file I/O, dirents, wd,
os.Executable, timers, console). Sockets, signals/VEH, os/exec are the
remaining chunks.

Recon baseline (linux, this branch): make.bash 3m10s; fat fizzbuzz +
runtimeprobe build; probe passes the full gauntlet ("ok all", exit 0).
Under wine 9.0 the wave-1 surface carries the probe through args/
environ/mark, then the first os.Getpid() dies at the designed 0xf7
crash poke (rawSyscallNoError has no NT route) — exit 0xC0000005.
That poke is wave 2's starting line.

## Wave 2 chunk A (2026-07-18): the syscall emulation layer

End state under wine 9.0: the probe prints ok for args/environ/mark,
getpid/getppid, numcpu, monotonic, sleep/ticker/after/ctxtimeout,
executable, mkdirtemp/statdir/create/readback/rename/statsize,
getwd/chdir/wdrestore, remove/rmdir, readdir/walkdir/removeall;
tcplisten FAILs gracefully (socket ENOSYS), execchild FAILs gracefully
(pipe2 ENOSYS); then the process dies at segvrecover with 0xC0000005
(no VEH yet — designed, and why the probe now runs the signal block
dead last). Linux stays 100% green (39 checks + ok all, rc 0);
apetest, fizzbuzz (linux+wine, redirected output byte-exact), and the
cosmo/arm64 std build all pass.

### Architecture: where emulation runs (THE load-bearing decision)

The darwin model (nosplit emulation between entersyscall/exitsyscall)
cannot work for NT: path translation (UTF-8→UTF-16) and Linux-struct
synthesis (stat, dirent64) must allocate, and entersyscall sets
throwsplit. So on NT the syscall package SKIPS entersyscall
(syscall_cosmo.go Syscall/Syscall6 check cosmo.Windows() first) and
the emulation runs as ordinary Go — the cgocall model: each genuinely
blocking Win32 call (ReadFile/WriteFile/FlushFileBuffers) brackets
ITSELF with entersyscall via runtime.ntcallSE, so sysmon can retake
the P during a blocking console read. Quick metadata calls stay plain
(ntcallE).

Pointer discipline that makes this sound: syscall arguments may point
into the calling goroutine's STACK, and raw uintptrs are not adjusted
when the stack moves. Rule: the chain from syscall.Syscall down to the
dispatcher entry is nosplit, and runtime.ntSyscallEmulate
(os_cosmo_nt_sys.go) re-types every pointer-carrying uintptr in the
dispatch call expressions themselves; from there the backends hold
real pointers (stack copying adjusts them); converting back to uintptr
happens only inside nosplit ntcallE/ntcallSE where no growth can
occur. GetLastError is fetched inside those same helpers — runtime
functions are never async-preempted and there is no preemptible
prologue between the call and the fetch, so the thread-local error
cannot be lost to a goroutine migration (re-audit when SuspendThread
preemption lands: the trampoline should then capture the error).

rawSyscallNoError became a Go wrapper (NT → table; else the renamed
rawSyscallNoErrorAsm keeps the SYSCALL fast path and the 0xf7 poke as
belt-and-suspenders). It is deliberately NOT nosplit: the darwin arm64
emulation chain under the asm entry sits exactly at the 792-byte
nosplit budget and an 80-byte wrapper frame broke the build ("nosplit
stack over 792 byte limit" through syscall6SlowDarwin). Same lesson at
the trampoline: widening ntcallArgs to 8 slots blew the
cgoSigtramp→…→write1→ntwrite1→ntcall→asmcgocall chain by 24-32 bytes,
so ntcall keeps the slim 6-arg block and a parallel ntcallArgs8/
ntcall8 trampoline (sys_cosmo_nt_amd64.s) serves 7-arg CreateFileW.

### The pieces (all in src/runtime unless noted)

- Dispatcher + backends + Win32-error→Linux-errno table:
  os_cosmo_nt_sys.go (ntSyscallEmulate; ntErrno holds the one errno
  mapping, cosmo's __dosemapping equivalent). Emulated: read write
  openat close stat lstat fstat newfstatat lseek pread64 pwrite64
  getdents64 mkdirat unlinkat renameat readlinkat(/proc/self/exe only)
  faccessat fchmod(at) chdir getcwd ftruncate fsync fdatasync fcntl
  getrandom getpid getppid gettid getuid geteuid getgid getegid(0)
  getpgrp(=pid) umask(022) exit exit_group. Everything else ENOSYS.
- fd table: os_cosmo_nt_fd.go. Fixed [512] array (no alloc on nosplit
  lookup paths, EMFILE when full), lowest-free unix semantics, slots
  0/1/2 seeded at boot from GetStdHandle and ALWAYS marked open (a
  dead handle fails per-op EBADF; keeps checkfds from raw-syscall
  open("/dev/null")). Entry: handle, kind (file/dir/stdio — sockets
  wave adds its kind here), GetFileType for stdio fstat synthesis,
  cloexec, Linux O_* flags, absolute Win32 pathW (for *at joins), and
  getdents state (dirStarted + pending entries). Lookups return
  copies; close-vs-op races have unix use-after-close semantics.
  runtime fcntl (fcntl_cosmo_amd64.go) consults the same table
  (F_GETFD/SETFD/GETFL/SETFL; else ENOSYS).
- Path policy: os_cosmo_nt_path.go, ONE documented function pair.
  ntPathW: "/dev/null"→"NUL"; "/tmp[/x]"→GetTempPathW()+x (cached;
  the magic that makes unix-shaped os.TempDir work); "/c[/x]"→"C:\x"
  (single-letter rule); "X:..." passthrough; other absolute → current-
  drive-rooted "\x"; relative passthrough; all with slash flip; \\?\
  prefixed only when a drive-absolute result exceeds MAX_PATH.
  ntPathToLinux: strips \\?\, "C:\a"→"/c/a" (drive lowercased, rest
  untouched), used by getcwd + readlink(/proc/self/exe) so
  Chdir(Getwd()) round-trips exactly. A /tmp path deliberately comes
  back as its real /c/ spelling — checkWd compares via os.SameFile,
  which works because stat fills dev/ino from VolumeSerialNumber +
  FileIndex.
- stat synthesis (os_cosmo_nt_sys.go ntStatFromInfo): Linux amd64
  Stat_t layout byte-exact; modes synthetic (dirs 0755, files 0755 or
  0555 when READONLY — everything "executable" since NT has no x bit
  and exec.LookPath gates on 0111); times from FILETIME (ctime :=
  CreationTime, documented approximation); Blksize 4096. chmod/
  fchmodat are validated no-ops (mapping mode&0200 onto READONLY would
  break later unlinks).
- getdents64: GetFileInformationByHandleEx(FileIdBothDirectoryInfo /
  RestartInfo on first query) — the dir HANDLE holds the kernel
  cursor, same shape as darwin's __getdirentries64 emulation. Records
  parsed into the fd's pending list, drained into the caller's buffer
  as Linux dirent64 (ino from FileId, |1 when 0 — ParseDirent skips
  ino==0; d_off 0; DT_DIR/DT_REG from attributes); nothing is lost
  when a batch outsizes the buffer.
- open mapping: access from O_ACCMODE (+FILE_READ_ATTRIBUTES always,
  so fstat works on O_WRONLY fds; O_APPEND → FILE_APPEND_DATA instead
  of GENERIC_WRITE = atomic appends); share always READ|WRITE|DELETE;
  disposition CREATE_NEW/CREATE_ALWAYS/OPEN_ALWAYS/TRUNCATE_EXISTING/
  OPEN_EXISTING per O_CREAT/O_EXCL/O_TRUNC; FILE_FLAG_BACKUP_SEMANTICS
  unconditionally (lets os.Open open directories); post-open classify
  via GetFileInformationByHandle (fail → stdio kind, e.g. NUL);
  O_DIRECTORY on non-dir → ENOTDIR, write-mode dir open → EISDIR.
- pread/pwrite: save/seek/transfer/restore around the shared file
  pointer (concurrent same-fd plain reads could observe the seek —
  internal/poll serializes per-fd, accepted + documented).
- Entropy: ProcessPrng (bcryptprimitives.dll, upstream Go's choice
  since 1.22; RtlGenRandom/advapi32 fallback, same signature),
  resolved gracefully at ntResolve. Backs SYS_GETRANDOM, runtime
  readRandom, and ntBootInit's upgrade of the RDTSC-mixed rt0
  AT_RANDOM bytes before randinit consumes them.
- os.Executable: zero os-package changes — the syscall layer answers
  readlinkat("/proc/self/exe") with GetModuleFileNameW in /c/ form
  (executable_cosmo.go tries exactly that readlink first).
- Identity: GetCurrentProcessId; getppid via ntdll
  NtQueryInformationProcess(ProcessBasicInformation) word 5, graceful
  ENOSYS if unresolvable; gettid = GetCurrentThreadId, and
  minitProcid now records real thread ids (procid becomes load-bearing
  in the preemption chunk).
- Console (ntBootInit): SetConsoleOutputCP/SetConsoleCP(CP_UTF8) +
  ENABLE_VIRTUAL_TERMINAL_PROCESSING on stdout/stderr where
  GetConsoleMode says they are consoles; fire-and-forget under
  redirection. Byte-exactness verified under wine with redirected
  stdout ("fizzbuzz\n", od-clean).
- **netpoll stub (recon assumption FALSIFIED)**: the recon claimed
  timers never initialize netpoll; in this Go version
  (*timers).addHeap unconditionally calls netpollGenericInit, so the
  first time.Sleep threw "netpollinit failed" (epollcreate ENOSYS).
  Fix: a third netpoll personality in netpoll_cosmo.go — NT stub:
  netpollinit succeeds creating nothing; netpoll(delay) parks the
  polling M on a WaitOnAddress futex word (ntNetpollBreakWord) for the
  timer delay; netpollBreak stores 1 + wakes it (compare-and-wait
  closes the lost-wakeup race); netpollopen/close ENOSYS;
  netpollIsPollDescriptor false. The sockets chunk replaces this with
  a real poller — keep the netpolldiag linkname compiling when doing
  so.

### Wine quirks (let CI be the judge later)

- getppid reports 0 under wine (InheritedFromUniqueProcessId comes
  back 0 for the boot process) — probe accepts >= 0; expect a real
  parent pid on windows-latest.
- Console CP/VT calls are unverifiable under wine-with-redirect;
  harmless there by construction (GetConsoleMode gates the mode set).

### Chunk-A commits

- 6a455e4e runtime, syscall: NT syscall emulation — identity,
  entropy, file I/O, dirents, timers (+ netpoll stub, fd table, path
  layer, ntcall8 trampoline, console boot setup)
- 968423f0 runtimeprobe: run the signal-dependent checks last
  (pre-VEH NT crashes at segvrecover; exec stays just before the
  block for wedge localization)

### What the next chunks must know

- The Emulate dispatcher contract (nosplit + re-type pointers at
  dispatch) applies to every new syscall case — add cases in
  ntSyscallEmulate, never a second entry path.
- ntcallE/ntcallSE take (fn, a1..a7); everything Win32 goes through
  them (or plain ntcall for boot/g0/no-error contexts). If a call
  needs >7 args (CreateProcessW has 10), widen ntcallArgs8/ntcall8 —
  NOT ntcallArgs (the write1 nosplit chain is at budget).
- Sockets: add an ntFDSocket kind + per-kind state to ntFDEntry;
  replace the netpoll stub wholesale; ntNetpollBreakWord and the
  break protocol can carry over if WSAPoll waits ride a break socket
  instead.
- exec: the wait-status protocol decision (cosmo encodes code<<8) is
  still open; runtime exit() passes plain codes today.
- Signals/VEH: TEB StackBase/StackLimit are still stale on pivoted
  threads (tstart_cosmo_nt) — fix alongside VEH; thread handles are
  still leaked by ntNewosproc (preemptM needs them retained).
- fcntl F_DUPFD_CLOEXEC (1030) is ENOSYS; os.dup paths will need
  DuplicateHandle eventually.
- Long relative-join paths skip the \\?\ check (only the absolute
  translation path applies it); fine for CI temp paths, revisit if a
  deep-tree workload appears.

## Wave 2 chunk B (2026-07-18): os/exec - pipe2, CreateProcessW, wait4

End state under wine 9.0: everything chunk A had stays ok AND
execchild passes (full loop: status+stdout pipes, self re-exec of the
pristine APE as a PE, child write+exit, parent EOF read, wait4
decode). tcplisten still FAILs gracefully (sockets are chunk C); the
run still dies at segvrecover with 0xC0000005 by design (VEH is chunk
D). Linux battery: probe 39 checks + ok all rc 0, apetest ok,
fizzbuzz linux+wine ok, cosmo/arm64 std builds, vet clean. A
dedicated exec stress harness (scratchpad, not committed) passed on
BOTH hosts: 6 sequential spawns, exit-code 42 decode, AV-child ->
SIGSEGV wait status (linux leg: GOTRACEBACK=crash -> SIGABRT),
ENOENT for a missing image, 9-arg quoting round trip through the
child's GetCommandLineW parse, env-block survival (incl. lower-case
names), attr.Dir, and 4 concurrent spawns.

### The chunk's one new BUG: direct win64 calls on 8-aligned stacks

os.Exit crashed with 0xC0000005 on NT - deterministically per binary
layout (one build always crashed, the next never) - while
return-from-main worked. Wine's +seh trace pinned the fault to
ntdll's __wine_setjmpex doing `movdqa %xmm6,0x60(%rcx)` with rcx+0x60
only 8-aligned (alignment GP faults report ExceptionInformation[1] =
-1, which looks like a wild read at 0xffffffffffffffff - don't chase
that pointer). RtlExitUserProcess __TRYs its DllMain notifications,
setjmp-ing a stack-local jmp_buf; the compiler 16-aligns that local
RELATIVE to an entry SP it may assume is 8 mod 16 (win64 ABI). Go
stacks are only 8-aligned, and the four direct-call NT branches in
sys_cosmo_amd64.s (exit, exitThread, usleep, osyield - everything
else rides asmcgocall, which ANDs SP to 16) passed the Go chain's
alignment straight through, so half of all layouts handed win64 code
a misaligned frame. Which call chains hit it depended on the frame
sizes between runtime.main / syscall_Exit and the asm - hence the
per-binary determinism (wine maps are static, no ASLR jitter). Fix:
all four sites now realign (exit paths: ANDQ $~15 + SUBQ $32; the
returning pair saves SP in SI, which win64 preserves). usleep/osyield
had the same latent bug and only survived because wine's Sleep happens
not to spill XMMs at an offending depth - real NT ntdll makes no such
promise; treat "SUBQ $40 discipline" as retired, realignment is
mandatory for every direct win64 CALL from Go-stack asm.

### Spawn design (the three seams)

- **pipe2 -> CreatePipe** (emulated syscall, fd table kind ntFDPipe).
  NULL SECURITY_ATTRIBUTES = handles born non-inheritable, which IS
  the O_CLOEXEC-shaped default: NT children inherit only the three
  explicitly duplicated std handles, never arbitrary fds, so cloexec
  is effectively always-on and the flag is only recorded for fcntl
  round-trips. O_NONBLOCK is recorded but pipes stay blocking:
  netpollopen keeps refusing pipe fds (ENOSYS), internal/poll's
  pd.init fails, FD.Init flips to blocking mode, and os.newFile
  restores the nonblock bit and ignores the error - the exact
  documented fallback (verified in-tree: internal/poll/fd_unix.go
  Init, os/file_unix.go newFile). ReadFile/WriteFile on pipes are
  ntcallSE-bracketed (a blocked reader parks its M in the kernel; the
  P is released). ERROR_BROKEN_PIPE on read = 0 bytes = EOF, matching
  Linux read()==0 on writer close; ERROR_NO_DATA on write maps to
  EPIPE (Go's no-SIGPIPE semantics come free - NT has no signals).
- **forkAndExecInChild -> ntForkExec** (exec_cosmo.go branches on
  cosmo.Windows() != nil before any fork machinery; exec_cosmo_nt.go).
  The syscall layer owns the string algebra, ported verbatim from
  upstream exec_windows.go: appendEscapeArg/makeCmdLine (backslash
  doubling before quotes, quote-if-space/tab/empty) and the
  case-insensitively sorted double-NUL UTF-16 env block, passed with
  CREATE_UNICODE_ENVIRONMENT. It reconstructs strings from the
  already-C-converted argv (BytePtrFromString rejected interior NULs,
  so the round trip is lossless), absolutizes a relative argv0
  against attr.Dir (CreateProcessW resolves the image against the
  PARENT cwd but chdirs the child - upstream joinExeDirAndFName
  parity), rejects >3 attr.Files and every fork-flavored SysProcAttr
  knob with ENOSYS (loud, documented - the probe uses none), and
  holds ntSpawnMu across the spawn: acquireForkLock only COUNTS
  concurrent forkers, and the spawn window contains temporarily
  inheritable duplicates that a concurrent bInheritHandles=TRUE
  CreateProcessW would capture (leaked pipe write end = deferred EOF
  = wedged sibling). The runtime hook (WindowsFns.Spawn = ntSpawn,
  os_cosmo_nt_exec.go) translates argv0/dir through the chunk-A path
  layer, DuplicateHandle(bInheritHandle=TRUE)s the three stdio
  handles, fills STARTUPINFOW(STARTF_USESTDHANDLES), calls
  CreateProcessW through the widened ntcall10 trampoline (ten args;
  ntcallArgs8/ntcall8 renamed and grown per the chunk-A rule - the
  6-arg ntcall block in the write1 nosplit chain is untouched),
  closes the dupes and hThread, and commits hProcess into a
  reserve-then-commit pid->handle table (ntProcMax=64; reservation
  makes a full table fail EAGAIN BEFORE the child exists instead of
  leaking an unwaitable process). lpCommandLine is a fresh mutable
  []uint16 every time - CreateProcessW is documented to scribble on
  it. PROC_THREAD_ATTRIBUTE_HANDLE_LIST hardening was considered and
  deliberately skipped (STARTUPINFOEXW size dance + the NULL-handle
  list-poisoning gotcha); ntSpawnMu gives the same process-local
  guarantee.
- **The status pipe protocol degenerates instead of being ported**:
  the child never inherits forkExec's O_CLOEXEC status pipe, so after
  the parent closes its write copy the read end has zero writers and
  ReadFile fails ERROR_BROKEN_PIPE instantly = EOF = "exec
  succeeded". Spawn failures never travel through the pipe at all -
  they return synchronously as forkAndExecInChild's errno. No
  child-side code exists on NT, period.

### wait4 and THE WAIT-STATUS PROTOCOL (design decision, recorded)

blockUntilWaitable costs nothing: package os calls waitid
(SYS_WAITID) first and its ENOSYS fallback is DOCUMENTED in
os/wait_waitid.go ("reportedly not available in Ubuntu on Windows" -
returns (false, nil), pidWait proceeds to plain Wait4). SYS_WAITID
stays ENOSYS on purpose; only SYS_WAIT4 is emulated.

wait4(pid): pid<=0 -> ECHILD (no wait-any/process groups on NT;
package os always names the pid). Handle from the pid table (missing
-> ECHILD), WaitForSingleObject with INFINITE - ntcallSE-bracketed,
so the waiting M sits in the kernel with its P released - or timeout
0 for WNOHANG (WAIT_TIMEOUT -> return 0). Only after the wait
reports signaled: GetExitCodeProcess (STILL_ACTIVE=259 ambiguity
never arises - a live process is never queried, so a child that
exit(259)'d decodes honestly), GetProcessTimes -> rusage
utime/stime (100ns Filetimes / 10 -> usec; all other rusage fields
zero, unknowable on NT - darwin-leg precedent), then reap: remove
table entry (losing a concurrent-reap race -> ECHILD, like a Linux
double wait) and CloseHandle.

Status packing (parent-side ONLY - the exit code crosses the process
boundary RAW, runtime exit() keeps passing plain codes to
ExitProcess, so cosmo children interoperate with native Windows
parents; the <<8 shape exists only inside our wait4):

    code < 0xC0000000              -> (code&0xff)<<8   WIFEXITED (Linux truncates to 8 bits; so do we)
    0xC0000005 ACCESS_VIOLATION    -> 11 SIGSEGV       (low 7 bits: WIFSIGNALED)
    0xC0000006 IN_PAGE_ERROR       ->  7 SIGBUS
    0xC000008D..0xC0000095 FLT_*/INT_DIVIDE/INT_OVERFLOW -> 8 SIGFPE
    0xC000001D ILLEGAL_INSTRUCTION ->  4 SIGILL
    0xC000013A CONTROL_C_EXIT      ->  2 SIGINT
    0xC0DE0000|signo (1..0x7F)     -> signo             fork-private, reserved for chunk D
    any other >= 0xC0000000        ->  9 SIGKILL

This mirrors the darwin leg's "wait4 with Linux-numbered wait
statuses" convention, so syscall_cosmo.go's linux WaitStatus algebra
(WIFEXITED = status&0x7f==0, signal = status&0x7f) decodes unchanged
and os/exec ExitError formatting just works ("signal: segmentation
fault" - wine-verified end to end via an AV child). The 0xC0DE base
is the contract with chunk D: dieFromSignal/kill emulation exits the
victim with ExitProcess(0xC0DE0000|signo) so waitsig can report
death by signals that have no NTSTATUS (SIGUSR1). It sits in NTSTATUS
severity-error space so foreign parents still see "crashed"; a
foreign child legitimately exiting with such a code aliases - accepted
(cosmo's own protocol aliases the same way).

### Probe-side change

selfCommand's magic sniff gains one branch: pristine-MZ + NT host ->
direct exec (the APE IS a valid PE; /bin/sh does not exist). Host
detection = env OS=Windows_NT (set by every NT since forever, and by
wine; absent on unix - linux/darwin behavior byte-identical, and the
recon's suggested mechanism). No new runtime surface.

### Wine quirks (watch on real NT, let CI judge)

- One concurrent-spawn run printed "wine client error:280: sendmsg:
  Bad file descriptor" while all checks still passed (3/3 clean
  reruns after) - wineserver-internal fd noise during parallel
  CreateProcessW+CloseHandle; nothing observable at the API level.
  If windows-latest shows exec flakes, suspect handle lifetimes
  first.
- wine's CreateProcessW quoting round-trips all 9 tricky-arg cases;
  real NT uses the same MSVCRT parse in the child (our own ported
  ntCommandLineToArgv), so drift risk is low.

### Chunk-B commits

- 0335e6e2 runtime: 16-align SP around direct win64 calls on NT
- 08e80bd1 runtime, syscall: NT os/exec - pipe2, CreateProcessW
  spawn, wait4

### What chunks C and D must know

- Chunk C (sockets/netpoll): netpollopen's pipe refusal is
  load-bearing for exec stdio - when the real poller lands, keep
  refusing non-socket fds so pipes stay in blocking mode (upstream
  windows has the same split). The fd table now has ntFDPipe; add
  ntFDSocket alongside. WSAStartup lazily; ntcall10 exists if any
  winsock call needs >7 args (WSAIoctl has 9). Keep
  runtime.cosmoNetpollDiag compiling.
- Chunk D (signals/VEH): encode signal deaths as
  ExitProcess(0xC0DE0000|signo) and TerminateProcess(h,
  0xC0DE0000|9) for kill(SIGKILL) - wait4 already decodes both.
  The stale-TEB consequence is now OBSERVED, not theoretical: wine
  refused to dispatch the setjmp AV ("Exception frame is not in
  stack limits") because RSP was a Go stack outside the
  TEB-declared window - fix TEB StackBase/StackLimit alongside VEH
  or continue-from-exception will misbehave the same way. GetLastError
  capture must move into the trampoline when SuspendThread preemption
  lands (chunk-A note stands). ntSpawnMu + the preemptExtLock design
  interact: bracket CreateProcessW-class calls per upstream's
  osPreemptExtEnter when preemption arrives.
- fd-table lifecycle proven: 10+ spawns (6 sequential + 4 concurrent)
  with 3 pipes each leak nothing (probe + stress rerun stable).

## Wave 2 chunk C (2026-07-18): sockets + the WSAPoll netpoller

End state under wine 9.0: everything chunks A+B had stays ok AND the
whole TCP block (tcplisten, tcpecho, deadline, tcpserver) plus udp
pass; unixsock FAILs gracefully with EAFNOSUPPORT because wine's
ws2_32 has no AF_UNIX support at all (see wine fidelity below) - the
afunix leg's judge is windows-latest CI. The run still dies at
segvrecover with 0xC0000005 by design (VEH is chunk D). Linux
battery: probe 39 checks + ok all rc 0 (the netpoll rewire leaves the
epoll path byte-untouched), fizzbuzz linux+wine byte-identical,
cosmo/arm64 std builds, vet clean, gofmt clean. A dedicated
readiness-model stress harness (scratchpad sockstress, not committed)
passed 3/3 under wine and natively on linux: live read-deadline
expiring DURING a blocked read (300ms observed), late data beating a
generous deadline, connect-to-closed-port -> prompt ECONNREFUSED (the
POLLERR/SO_ERROR completion path), 8 concurrent echo connections x 50
round trips, 4 MiB each way through one connection (send-buffer
backpressure -> waitWrite/POLLWRNORM), a UDP read deadline, close(2)
promptly unblocking a parked reader, and an Accept deadline.

### Winsock surface (os_cosmo_nt_sock.go)

Lazy bring-up: ntWinsockEnsure() LoadLibraryA("ws2_32.dll") +
GetProcAddress x19 + WSAStartup(0x202) exactly once, mutex-guarded
with a sticky failure state, triggered by whichever runs first of the
socket(2) emulation and netpollinit (the two can race; a non-network
program never loads winsock - though any timer-using program now does,
via netpollinit's wake socket, the cost of replacing the stub
wholesale). Nothing in the ensure path allocates: netpollGenericInit
can fire from the first timer's addHeap under runtime locks.

Creation is WSASocketW WITHOUT WSA_FLAG_OVERLAPPED and WITH
WSA_FLAG_NO_HANDLE_INHERIT: plain BSD-shaped synchronous calls
(recv/send/recvfrom/sendto/accept/connect), no OVERLAPPED machinery
anywhere - upstream's IOCP netpoll answers "did my submitted
operation complete", not "is this fd readable", so it cannot back a
linux-shaped internal/poll; WSAPoll can. SOCK_NONBLOCK/SOCK_CLOEXEC
are stripped and emulated (ioctlsocket FIONBIO after create;
cloexec = the no-inherit creation flag, recorded for fcntl).
fcntl(F_SETFL) pushes O_NONBLOCK changes into FIONBIO for
socket-kind fds. Accepted sockets get SetHandleInformation
(HANDLE_FLAG_INHERIT, 0) - their inheritability is not contractually
specified and chunk B's spawn uses bInheritHandles=TRUE.
close(2) on the ntFDSocket fd-table kind routes to closesocket
(CloseHandle would leak winsock provider state); read/write route to
recv/send; fstat synthesizes S_IFSOCK|0777; lseek reports ESPIPE.

### Translation tables (the load-bearing deltas)

- AF values: AF_UNIX=1 and AF_INET=2 match Linux; **AF_INET6 is 23 on
  NT vs 10 on Linux** - rewritten inside every sockaddr in both
  directions (layouts are otherwise byte-identical; no sa_len on NT,
  so less work than the darwin leg's 10<->30).
- Sockopts (curated, unknown pairs -> ENOPROTOOPT): SOL_SOCKET 1 ->
  0xffff with SO_REUSEADDR 2->4 (NT semantics are looser -
  REUSEADDR+REUSEPORT-ish - fine for listeners), SO_TYPE 3->0x1008,
  SO_ERROR 4->0x1007 (the RETURNED VALUE is translated winsock->Linux
  errno too - the nonblocking-connect completion protocol depends on
  it), SO_BROADCAST 6->0x20 (net's setDefaultSockopts REQUIRES this
  to succeed on UDP), SO_SNDBUF 7->0x1001, SO_RCVBUF 8->0x1002,
  SO_KEEPALIVE 9->8, SO_LINGER 13->0x80 with the struct converted
  between Linux {i32,i32} and winsock {u16,u16}; IPPROTO_TCP (=6 both
  sides): TCP_NODELAY 1->1, TCP_KEEPIDLE 4->3, TCP_KEEPINTVL 5->17,
  TCP_KEEPCNT 6->16; IPPROTO_IPV6 (=41 both): IPV6_V6ONLY 26->27.
- Errnos: winsock failures land in the SAME TEB last-error slot as
  every Win32 call (WSAGetLastError is a reader of that slot), so
  ntcallE/ntcallSE capture them with zero extra machinery; ntWSAToLinux
  maps the WSAE* range (10035 WSAEWOULDBLOCK->EAGAIN, 10047->
  EAFNOSUPPORT, 10054->ECONNRESET, 10061->ECONNREFUSED, ...).
  Special cases: connect's WSAEWOULDBLOCK -> **EINPROGRESS** (winsock's
  spelling of a pending nonblocking connect; WSAEINPROGRESS is a
  winsock-1.1 artifact), accept's WSAECONNRESET -> **ECONNABORTED**
  (linux semantics; internal/poll's accept loop swallows and re-polls
  it), recv-side WSAESHUTDOWN -> EOF while send-side -> EPIPE, and
  recvfrom's WSAEMSGSIZE (truncated datagram) is swallowed into a
  full-buffer read like Linux's silent truncation.
- UDP: SIO_UDP_CONNRESET + SIO_UDP_NETRESET are disabled (WSAIoctl
  via the chunk-B ntcall10 trampoline - 9 args) on every INET dgram
  socket at creation, best-effort (wine lacks the ioctls): otherwise
  a latched ICMP unreachable from an earlier send fails unrelated
  recvs with WSAECONNRESET. Upstream net does the same.

### AF_UNIX

Pathname SOCK_STREAM over afunix.sys (Win10 17063+). sun_path crosses
into winsock as a **UTF-8 Windows path** (afunix's documented
encoding), produced by the chunk-A path layer (ntPathW -> UTF-8), so
"/tmp/x.sock" binds the real temp-dir file; >107 translated bytes ->
EINVAL. The Linux-spelling name is RECORDED in the fd table
(unixBound at bind, unixPeer at connect - including the EINPROGRESS
arm) and getsockname/getpeername report the recorded bytes: Linux
returns exactly what you bound, and back-translating winsock's stored
Windows path would surface the /c/... alias (the probe compares addr
STRINGS - os.SameFile does not apply to socket names). Unnamed
sockets report the 2-byte family-only sockaddr, which the
linux-shaped anyToSockaddr renders as the empty name (the probe's
unnamed-dialer canary); the caller's buffer is zeroed first because
that decoder scans the whole 108-byte sun_path. Abstract-namespace
names (leading NUL) and autobind (empty path) are refused EINVAL
exactly like the darwin leg. Socket FILES are reparse points
(IO_REPARSE_TAG_AF_UNIX) and are NOT auto-deleted on close;
unlink/RemoveAll delete them like ordinary files via DeleteFileW (the
probe's own deferred RemoveAll covers its temp dir; verified in the
existing unlinkat emulation - no special casing needed).
getsockname failing WSAEINVAL on an unbound socket (winsock refuses
what Linux answers) is synthesized into the Linux zero-address reply.

### Netpoll: WSAPoll, aix-shaped, level-triggered (netpoll_cosmo_nt.go)

Chunk A's WaitOnAddress timer-only stub is deleted wholesale; NT now
has a third real poller personality behind the netpoll_cosmo.go
host dispatch (netpollinitNT/openNT/closeNT/armNT/BreakNT/NT).
Design = netpoll_aix.go line-for-line where possible:

- **State**: fixed arrays [513]WSAPOLLFD/[513]*pollDesc/[513]int32
  (fd-table-sized + wake slot; NO slices - netpollinit and
  netpollopen can run under runtime locks, so nothing allocates).
  WSAPOLLFD is 16 bytes on win64 (8-byte SOCKET + two SHORTs + pad),
  NOT Linux's 8-byte pollfd, and the POLL* constants differ
  (POLLRDNORM=0x100, POLLWRNORM=0x10, POLLERR=1, POLLHUP=2,
  POLLNVAL=4). Only RDNORM/WRNORM are ever REQUESTED - WSAPoll
  rejects POLLERR/POLLHUP/POLLPRI in events with WSAEINVAL.
- **Two-lock protocol**: mutators take ntMtxpoll, send one byte to
  the wake socket if no update is pending, take ntMtxset (held by the
  poller across its blocking WSAPoll), release ntMtxpoll, mutate,
  release. The poller re-takes both at cycle top and clears
  pendingUpdates. pd.user holds the slot index, maintained on
  swap-delete; netpollclose is keyed by the emulated fd number
  (parallel ntPollNums array) since slots store SOCKET handles.
- **Level-triggered arming**: netpollinitNT sets
  netpollLevelTriggered (the hook netpoll.go kept from the old darwin
  poll(2) poller), so pollWait arms the awaited direction each wait
  and delivery disarms it (events &^= bit). netpoll(0) returns empty
  (aix rule: a nonblocking check would contend ntMtxset with the
  blocked poller); sysmon/findrunnable tolerate that by precedent.
- **Wake socket**: WSAPoll has no pipe/eventfd concept, so slot 0 is
  a nonblocking loopback UDP socket bound to 127.0.0.1:0 and
  connect()ed TO ITSELF (sends need no address; connectedness
  filters foreign datagrams). netpollBreak = the shared netpollWakeSig
  CAS + one send; the poller drains with nonblocking recvs and resets
  the sig only when it was blocking (aix rule).
- **netpollopen refuses non-socket fds** (ENOSYS -> internal/poll
  blocking fallback): load-bearing for chunk B's exec stdio pipes,
  and WSAPoll would report non-sockets POLLNVAL anyway. POLLNVAL on a
  registered slot (close racing registration) disarms both directions
  and wakes both sides with the error bit, so a dead slot cannot spin
  the poller.
- **Forensics**: the wave-9 counters (xnuPollCycles/Done/Enter/Exit/
  LastN/LastE) are fed from the WSAPoll cycle too - exactly one host
  poller is live per process - so cosmoNetpollDiag names a stall in
  the runtimeprobe watchdog output on NT as well.
- **Timers**: ride the same netpoll(delay) timeout (ms, capped 1e9)
  plus netpollBreak wakes; the probe's sleep/ticker/after/ctxtimeout
  stayed green through the stub->WSAPoll swap, and the stress
  harness pins live deadlines at ~300ms observed.

GetLastError discipline: netpollNT runs on g0; the WSAPoll error
fetch is a second plain ntcall on the same thread with no preemption
possible between (runtime code is never async-preempted) - same
argument as ntcallE, re-audit when chunk D lands SuspendThread.

### Wine fidelity notes (let windows-latest judge)

- **AF_UNIX does not exist in wine's ws2_32**: WINEDEBUG=+winsock on
  a minimal socket(AF_UNIX=1, SOCK_STREAM) probe shows wine itself
  refusing it - "WSASocketW family 1, type 1 ... failed to initialize
  socket, status 0xc001273f" (facility-winsock NTSTATUS carrying
  0x273f = 10047 = WSAEAFNOSUPPORT) - while family 2 succeeds through
  the identical path. The probe's unixsock check therefore FAILs
  gracefully under wine with "address family not supported by
  protocol" and skips the unixecho leg; real NT (Server 2022 ships
  afunix.sys) is the judge. No design contortion for wine.
- SIO_UDP_CONNRESET/NETRESET are unimplemented in wine; the disable
  is fire-and-forget, and wine's un-Windows-like UDP behavior does
  not latch ICMP resets anyway (the probe's UDP check passes).
- Everything else matched real-NT semantics closely enough that the
  whole stress battery behaves identically on wine and linux,
  including error strings (ECONNREFUSED on refused connect, "use of
  closed network connection" on close-during-read).

### Chunk-C commits

- 413f271b runtime: NT sockets - winsock syscall emulation and
  WSAPoll netpoller

### What chunk D must know from chunk C

- The netpoller parks Ms INSIDE blocking WSAPoll on g0.
  SuspendThread-based preemption must not care (gFromSP finds g0,
  wantAsyncPreempt says no), but exit()'s suspendLock discipline now
  also covers a thread sitting in ws2_32 - same as any foreign call.
- netpollBreak/netpollarm/netpollopen make foreign calls (send on
  the wake socket) under ntMtxpoll; when osPreemptExtEnter brackets
  land, these are runtime-internal paths (g0/lock context) and must
  NOT take the preempt-ext lock - mirror upstream's stdcall-vs-
  cgocall split.
- Signal-driven wakeups do not exist: nothing in the poller depends
  on EINTR (WSAPoll has no alertable wait), so chunk D's VEH work
  cannot perturb it. If chunk D adds a console-ctrl relay M, wake it
  via its own event, not the netpoll wake socket.
- ntWinsockEnsure is idempotent and callable from any user-goroutine
  or init context; if chunk D ever needs a socket (it should not),
  call it rather than assuming winsock is up.
- waitsig stays red until chunk D: the probe's signal block is
  unchanged and still crashes at segvrecover (rc=5) by design.

## Wave 2 chunk D1 (2026-07-18): VEH -> sigpanic, self-signals, encoded deaths

End state under wine 9.0: everything chunks A+B+C had stays ok AND
the signal block comes alive - segvrecover (nil deref -> VEH ->
sigpanic -> recover), sigterm + sigusr2 (kill(self) -> trampoline ->
os/signal Notify), and waitsig (child kill(self, SIGKILL) ->
ExitProcess(0xC0DE0009) -> parent wait4 decodes "killed by signal
9"). Remaining red: preempt (chunk D2 - fails by duration, ~79s under
wine while the spin loops drain their iteration bound, then the rest
of the probe still runs) and unixsock (wine ws2_32 gap, chunk C).
Linux battery: probe 39 checks + ok all rc 0 (the rt_sigaction leg is
byte-untouched at runtime - sysSigaction's NT branch is behind
iswindows()), fizzbuzz wine+linux byte-identical, cosmo std builds on
amd64 AND arm64, changed files gofmt clean, vet clean for the new asm
decls, chunk-C sockstress battery 8/8 + ok all under wine with the
VEH live (exceptions x poller interaction).

### TEB stack-bounds policy: the WIDE window (decision + evidence)

Policy: NT_TIB.StackBase = 0x00007FFFFFFF0000, StackLimit = 0x10000
("everything user-mode is stack"), written (a) for the boot thread in
ntInitSignals and (b) in tstart_cosmo_nt right after the stack pivot
(constants shared via go_asm.h so the policy lives in one place,
os_cosmo_nt_sig.go).

Why not per-thread g0 bounds: user goroutine stacks are heap-allocated
and MOVE (copystack), so no fixed per-thread range can cover every RSP
the exception machinery will see; upstream's sigresume workaround
(signal_windows.go:174-192) exists precisely because its resume SP
must lie inside the TEB window - with the wide window the modified
CONTEXT can resume straight onto the faulting goroutine stack and the
whole sigresume dance is dropped.

Wine 9.0 evidence (12-cell experiment, scratchpad tebexp, on a
PIVOTED CreateThread thread): TEB mode {bogus 0x20000..0x10000 window
modeling the wave-1 stale bounds | per-thread g0 bounds | wide} x
fault stack {user goroutine | g0 via systemstack} x proof {dispatch:
first-position VEH exits 0x77 | continue: VEH bumps CONTEXT.Rip past
a known 2-byte faulting load, returns CONTINUE_EXECUTION, and a
store-after-fault flag proves NtContinue accepted the modified
context}. Result: ALL 12 cells pass - rc=0x77 for every dispatch
cell, "resumed=1" for every continue cell. So wine dispatches and
continues regardless of the TEB window (its virtual_setup_exception
only warns on out-of-limits stacks) and CANNOT distinguish the
policies; the wide choice rests on real-NT behavior, where the
continue path is documented (by upstream's own workaround) to
validate the resume SP against the TEB bounds. Wide is the one policy
whose correctness does not depend on which validations real NT
enforces - windows-latest CI is the judge. Note the chunk-B observed
refusal ("Exception frame is not in stack limits") was wine's SEH
frame walk, not VEH dispatch - consistent with these cells.

Residual caveats, accepted + documented: (1) the boot thread's REAL
loader stack has guard pages; if m0's g0 ever grew below the
PE-committed region, the kernel's guard-fault growth would rewrite
StackLimit back to a real value on that thread (harmless: that is
upstream's normal situation); the PE header commits the whole window
rt0 uses, so it does not trigger. (2) Foreign SEH inside kernel32
calls now validates frames against the wide window while on our
stacks - strictly more permissive than the stale bounds that wine
refused in chunk B.

stackSystem: cosmo now reserves the windows-equal 4096 bytes per
stack (stack.go) because the NT exception dispatcher writes
EXCEPTION_RECORD + CONTEXT + dispatcher state (up to ~4KiB with
extended machine state) BELOW the faulting RSP on the goroutine stack
itself - Go stacks have no guard pages, so a fault near the stack
bottom would otherwise corrupt adjacent heap. Verified: no mirror of
stackSystem exists in cmd/ (the linker's nosplit budget is
StackNosplitBase*multiplier, unaffected), so this is a runtime-only
constant - no make.bash needed. Cost: initial goroutine stacks go
2KiB -> 8KiB (fixedStack rounding) on every cosmo host; accepted -
one build serves all hosts, same provisioning philosophy as the fat
APE, and the linux probe stayed green.

### VEH -> sigpanic (os_cosmo_nt_sig.go + sys_cosmo_nt_amd64.s)

- Registration at ntBootInit (ntInitSignals, FIRST thing in boot):
  SetErrorMode(|= FAILCRITICALERRORS|NOGPFAULTERRORBOX|
  NOOPENFILEERRORBOX) + best-effort WerGetFlags/WerSetFlags
  (FAULT_REPORTING_NO_UI; the pair is missing on wine and resolved
  gracefully) so CI can never hang on a crash dialog; then
  AddVectoredExceptionHandler(1, ntExceptionTramp) +
  AddVectoredContinueHandler(1, ntFirstVCHTramp) +
  AddVectoredContinueHandler(0, ntLastVCHTramp) - upstream
  initExceptionHandler's amd64 shape.
- Thunks (ntsigtramp<> common body): win64 entry CX=EXCEPTION_POINTERS,
  DX=kind. The cosmo build's PUSH_REGS_HOST_TO_ABI0 compiles in its
  SysV flavor, which under-saves for a win64 caller, so the thunk
  hand-saves the full win64 callee-saved set the Go ABI clobbers
  (flags/DF, BP BX DI SI R12-R15, X6-X15), loads g from gs:0x28 into
  R14 (nil on a foreign thread -> CONTINUE_SEARCH), zeroes X15, and
  calls the nosplit ntSigtrampGo via its ABI0 wrapper; verdict back
  in EAX.
- ntSigtrampGo runs the handler on g0 via systemstack (upstream
  sigtrampgo's shape) - the VEH itself runs on the faulting stack,
  which is why stackSystem matters. No sigresume postlude (wide TEB).
- ntExceptionHandler: gate = PC inside Go text AND code in upstream
  isgoexception's exact set; translation table NT->Linux:
  0xC0000005->SIGSEGV/_SEGV_MAPERR, 0xC0000006->SIGBUS/_BUS_ADRERR,
  0xC0000094->SIGFPE/_FPE_INTDIV, 0xC0000095->_FPE_INTOVF, FLT_
  {DIV,OVF,UND,INEXACT,DENORMAL}->SIGFPE/{_FPE_FLTDIV,FLTOVF,FLTUND,
  FLTRES,FLTINV}, 0x80000003->SIGTRAP, 0xC000001D->SIGILL. throwsplit
  || isAbort (NT reports RIP one byte AFTER the INT3: isAbortPC(pc-1))
  || sigtable[sig].flags&_SigPanic==0 -> ntWinthrow: the last clause
  keeps LINUX semantics for SIGTRAP/SIGILL (throw-class on the fork's
  sigtable; upstream windows would push its own sigpanic for them,
  but the fork's linux sigpanic would PANIC with the signal name,
  which Linux never does). Otherwise record gp.sig (LINUX number),
  gp.sigcode0 (linux si_code), gp.sigcode1 (fault addr, AV/in-page
  only), gp.sigpc; push the fake sigpanic0 call frame (SP-=8, store
  resume PC) unless RIP==0 or RIP==asyncPreempt entry (issue #35773
  interlock - live the moment D2 lands), set RIP=sigpanic0; return
  CONTINUE_EXECUTION. The fork's linux-shaped sigpanic
  (signal_unix.go) then runs on the faulting goroutine: nil deref ->
  panicmem -> recoverable, exactly the linux path.
- ntFirstContinueHandler stops the continue-handler walk for handled
  exceptions (the re-check passes because the rewritten RIP =
  sigpanic0 is in Go text and the code is unchanged);
  ntLastContinueHandler -> ntWinthrow for exceptions nothing handled.
  (Whether wine invokes last-VCHs on the unhandled path is untested -
  an unhandled fault under wine dies with the raw NTSTATUS either
  way, which chunk B's parent-side wait4 map already translates; real
  NT gets the upstream-shaped crash report.)
- ntWinthrow = upstream winthrow (panicking gate, g0 stack-bounds
  blow-away, "Exception c0000005 ..." + sigtable name line,
  tracebacktrap + tracebackothers + ntDumpregs) EXCEPT the exit:
  ntExitEncoded(sig) - RaiseFailFastException would surface the raw
  NTSTATUS; the encoding keeps every signal death uniform for wait4
  (unmapped codes reaching the last VCH encode SIGKILL, matching
  wait4's unknown-NTSTATUS catch-all). Deliberate divergence from the
  linux leg, which exits 2 after a fatal-signal report: NT parents
  see "killed by signal N" - more informative, and mandated by the
  chunk-D contract.

### Self-directed signals (kill/tkill/tgkill)

- sysSigaction routes the NT leg to ntSigaction: a runtime-side
  [65]sigactiont table (there IS no kernel sigaction on NT; the only
  installers are initsig/sigenable/sigdisable/sigignore, serialized
  by construction, so plain stores suffice). getsig/setsig round-trip
  through it, so initsig's fwdSig bookkeeping and signal.Ignore/Reset
  behave exactly as on Linux. rt_sigprocmask/sigaltstack keep their
  wave-1 no-op asm stubs (masks are meaningless with synchronous
  self-delivery; gsignal keeps its malg bounds because !iscgo makes
  minitSignalStack take the signalstack() path). The dispatcher-level
  rt_sigaction(13) stays ENOSYS on purpose - os/signal never issues
  it; only the runtime installs handlers.
- Dispatcher cases kill(62)/tkill(200)/tgkill(234)
  (os_cosmo_nt_sys.go) -> ntEmuKill/ntEmuTkill/ntEmuTgkill:
  * pid == self: ntKillSelf runs the kernel decision tree against
    ntSigActs: sig 0 -> ok; SIGKILL -> ntExitEncoded now
    (uncatchable); SIG_IGN -> drop; SIG_DFL -> drop for the kernel
    default-ignore set (CHLD, URG, WINCH, CONT) AND the stop family
    (STOP, TSTP, TTIN, TTOU - job control does not exist on NT and
    nothing could ever deliver the resuming SIGCONT, so stops are
    dropped, documented divergence), else ntExitEncoded (default
    action = terminate); an installed handler -> DELIVER (below).
  * pid == a chunk-B child: sig 0 probes; else TerminateProcess(h,
    0xC0DE0000|sig), best-effort (already-dead child = success, like
    Linux kill on a zombie); the handle stays in the table - wait4
    still owns reaping. Unknown pid / pid<=0 (no process groups) ->
    ESRCH.
  * tkill/tgkill: only the CALLING thread is addressable in D1
    (cross-thread needs D2's SuspendThread machinery; the runtime's
    signalM stays gated off, and process-level observables are
    thread-agnostic); other tids -> ESRCH. tgkill checks tgid ==
    own pid.
- Delivery (ntDeliverSelfSignal + ntSignalTramp asm): synthesize a
  linux-format siginfo{si_signo, si_code=_SI_TKILL} (sigFromUser()
  true, like a real tgkill) and a zeroed ucontext with rip/rsp = the
  caller's real PC/SP (feeds the fatal-report prints; doSigPreempt's
  isAsyncSafePoint always refuses a "runtime." PC, so the dead
  context is never rewritten), switch SP to THIS M's gsignal stack
  top, and CALL the RECORDED handler - in practice the C-ABI
  sigtramp - with the linux handler signature (DI,SI,DX). From there
  everything is the stock fork path: sigtrampgo (SP inside gsignal ->
  no adjustSignalStack detour) -> sighandler -> sigsend for
  watched/notify signals, dieFromSignal for unwatched fatal ones,
  signal_ignored, throw-class fatal reports. dieFromSignal's raise()
  lands in the rewritten raise_nt -> ntExitEncoded, so unwatched
  SIGTERM et al die with the encoded status. The gsignal stack is
  otherwise unused on NT and delivery is synchronous, so borrowing it
  is sound; it is exactly where the Linux kernel would deliver
  (minitSignalStack installs gsignal as the alt stack).
- raise/raiseproc NT branches (sys_cosmo_amd64.s) tail-JMP
  ntExitEncoded (ANDL $0x7F | ORL $0xC0DE0000 -> ExitProcess, SP
  realigned per the chunk-B direct-call discipline). raise is only
  reached on die-paths by construction; crash() (GOTRACEBACK=crash)
  therefore exits 0xC0DE0006 = "killed by SIGABRT", matching the
  linux leg's observable.

### waitsig end to end (wine-verified)

probe parent --exec--> child "waitsig raise" --raiseFatalChild:
syscall.Kill(getpid(), SIGKILL)--> child dispatcher ntEmuKill(self,9)
-> ntExitEncoded(9) -> ExitProcess(0xC0DE0009) -> parent wait4
(chunk B) sees 0xC0DE0000|9 -> status 9 -> WaitStatus.Signaled() &&
Signal()==SIGKILL -> "ok waitsig killed".

### Wine fidelity notes (windows-latest judges)

- Wine's dispatch/continue paths ignore the TEB window (evidence
  above); real NT is stricter on continue - the wide window is
  designed for that, but only CI proves it.
- Wine's last-VCH invocation on the unhandled path is unverified
  (nothing in the probe exercises it).
- The preempt check fails by DURATION under wine (~79s: the spin
  loops drain their 20e9-iteration bound, GC completes late, then
  the probe continues; total run ~85s sits just under the probe's
  90s watchdog). D2's async preemption turns this into ~150ms; if
  windows-latest CPUs drain slower and the watchdog fires before
  waitsig prints, that is the watchdog working as designed - D2
  removes the condition.

### Chunk-D1 commits

- fb41da03 runtime: NT signals - VEH sigpanic, self-signal delivery,
  encoded deaths

### What chunk D2 must know (preemption + console ctrl)

- Real per-M thread handles + procid: ntNewosproc still LEAKS the
  CreateThread handle and mp.thread does not exist; preemptM needs
  minit to DuplicateHandle(GetCurrentThread) into an mOS field plus
  the threadLock/preemptExtLock/suspendLock protocol
  (upstream-windows-notes.md section 2). minitProcid already records
  real tids.
- GetLastError capture must move INTO the call thunk (ntcall6/
  ntcall10) once SuspendThread preemption exists: today ntcallE's
  second ntcall on the same thread is safe only because runtime code
  is never async-preempted between them (chunk A note; re-audit
  EVERY two-call error fetch: ntcallE, ntcallSE, netpollNT's WSAPoll
  error read).
- osPreemptExtEnter/Exit brackets around CreateProcessW-class foreign
  calls (the ntSpawnMu window) per upstream's cgocall model;
  runtime-internal paths (netpoll wake sends, ntcall from g0) must
  NOT take the bracket - the stdcall-vs-cgocall split.
- The VEH's asyncPreempt interlock is already in place: a fault with
  RIP == asyncPreempt entry does not push a second sigpanic frame
  (issue #35773). preemptM's PushCall must keep using CONTEXT_CONTROL
  and the isAsyncSafePoint-adjusted resume PC.
- Poller Ms park inside WSAPoll on g0: SuspendThread on them is
  harmless (gFromSP -> g0 -> wantAsyncPreempt false), but exit()'s
  suspendLock discipline must cover ws2_32-parked threads.
- Console ctrl: SetConsoleCtrlHandler is already resolved
  (ntSetConsoleCtrlHandlerFn), and CreateEventW/SetEvent are in the
  table for the relay-M design (upstream-windows-notes.md section 8:
  handler runs on an INJECTED thread with no g - the asm handler must
  stay Go-free; g==nil in ntsigtramp<> already returns
  CONTINUE_SEARCH for such threads, proving the TLS-nil path). Feed
  sigsend(SIGINT/SIGTERM) from a Go relay M, NOT from the injected
  thread; give the relay its own event, never the netpoll wake
  socket. ntKillSelf/ntDeliverSelfSignal are reusable for a
  same-thread synthesized delivery if the relay M design wants it.
- ntSigActs is the handler-state oracle: console-ctrl "unwatched ->
  default death" decisions can consult it exactly like ntKillSelf.
- CI flip (runtimeprobe on windows-latest) stays chunk E.

## Wave 2 chunk D2 (2026-07-18): async preemption + console control

End state under wine 9.0: the probe's preempt check goes GREEN -
163/173/181/166ms across four consecutive runs (linux reference
142ms), whole probe ~1.2s end to end (was ~85s while the spin loops
drained their iteration bound). unixsock is the SOLE remaining wine
red (ws2_32 AF_UNIX gap, chunk C's documented wine-fidelity case;
windows-latest judges), rc reflects only it. Chunk-C sockstress
battery 8/8 + ok all under wine WITH preemption live; fizzbuzz
wine+linux byte-identical. Linux native: 39 checks + ok all rc 0,
four consecutive runs, preempt 142-160ms - byte-untouched behavior.
cosmo std builds on amd64 AND arm64; changed files gofmt clean.

### Per-M thread handles (minit/unminit/ntNewosproc)

- mOS (os_cosmo_amd64_m.go) grows upstream's preemption trio:
  `thread uintptr` (a DuplicateHandle of the thread, made BY the
  thread itself in minit's NT leg - ntMinitThread), `threadLock
  mutex` guarding it, `preemptExtLock uint32` (the external-code CAS
  gate). unminit's NT leg closes the handle under threadLock and
  zeroes it, so ntPreemptM treats a dying M as unpreemptible (mexit
  and dropm both route through unminit). arm64's mOS gets only
  preemptExtLock (the shared osPreemptExtEnter/Exit must compile
  there; iswindows() is constant false so they dead-code away).
- ntNewosproc now closes the CreateThread handle immediately
  (wave 1 leaked it; the per-M handle is minit's duplicate, exactly
  upstream newosproc's split) and ports the CreateThread-vs-
  ExitProcess race freeze: CreateThread failing while ntExiting is
  set parks the thread on the double-locked ntDeadlock mutex
  (upstream issue #18253).

### Trampoline last-error capture (the D1 handoff's hard rule)

ntcall6/ntcall10 now bracket the foreign call themselves: zero the
TEB LastErrorValue (gs:0x30 -> TEB+0x68) before the CALL (upstream
asmstdcall's SetLastError(0)), and immediately after the target
returns, read it back and store it into this M's mOS.ntLastError
(reached from the trampoline via get_tls/g/g_m - cosmo amd64 keeps g
at gs:0x28 on both hosts, cmd/internal/obj/x86 Hcosmo). The capture
is atomic with the call: no window in which SuspendThread preemption,
a later win64 call, or any scheduling event can lose the value.
ntcallE/ntcallSE/ntcallSE10 read getg().m.ntLastError (single
trampoline trip now - the second GetLastError ntcall is gone), and
netpollNT's WSAPoll error read does the same. The ntcallArgs blocks
did NOT widen (the chunk-A nosplit rule): the store rides the m,
not the args frame.

### preemptM port (os_cosmo_nt_preempt.go ntPreemptM)

Upstream os_windows.go:1152-1236 carried over with the fork's
idioms; every upstream lock and ordering kept:

1. self-preempt throw; CAS mp.preemptExtLock 0->1 or fail the
   attempt (external code running).
2. mp.threadLock: mp.thread==0 -> unpreemptible, ack and return;
   else DuplicateHandle our own reference; unlock BEFORE suspending.
3. 16-byte-aligned FULL 1232-byte CONTEXT (ntContext grew its
   fltsave/vector tail; the buffer is over-allocated +15 and rounded,
   upstream's idiom), ContextFlags = CONTEXT_CONTROL only -
   asyncPreempt saves everything else itself.
4. ntSuspendLock serializes ALL SuspendThread callers (SuspendThread
   is asynchronous: two threads suspending each other deadlock).
   Held across SuspendThread AND GetThreadContext - the latter is
   what actually blocks until the suspension completes. SuspendThread
   returning -1 (thread gone) unwinds and acks.
5. Safe-point gate: ntGFromSP (upstream gFromSP - which of
   g0/gsignal/curg owns the interrupted SP) + wantAsyncPreempt +
   isAsyncSafePoint(pc, sp, 0). Threads inside win64 calls sit on
   their g0 stack (asmcgocall switched) -> gFromSP says g0 ->
   wantAsyncPreempt false -> plain suspend/resume, no injection;
   runtime-Go PCs are refused by isAsyncSafePoint's name check.
6. Injection = upstream PushCall's fake CALL: SP-=8, [SP]=the
   isAsyncSafePoint-ADJUSTED resume PC, RIP=asyncPreempt entry,
   SetThreadContext. (The VEH side's RIP==asyncPreempt interlock has
   been live since D1 - a fault-vs-preempt collision cannot
   double-frame.)
7. Release preemptExtLock; ack: preemptGen.Add(1) THEN
   signalPending.Store(0) - doSigPreempt's exact order. The clear is
   the fork-specific delta: the unix preemptM wrapper
   (signal_unix.go) CASed signalPending before signalM, and on
   signal hosts the handler's ack clears it; without the clear here
   the gate would stay shut and preemption would fire exactly once.
8. ResumeThread; CloseHandle.

Wiring: signalM's NT leg (os_cosmo.go) dispatches sigPreempt ->
ntPreemptM and drops everything else (preemptM is signalM's only
cosmo caller - grep-verified; secret.go's noopSignal is linux-only).
sysmon's retake, preemptone, suspendG, and STW all flow through
preemptM unchanged. preemptMSupported was already true
(signal_unix.go).

Lock order: threadLock -> (released) -> ntSuspendLock. Nothing takes
them nested the other way; a suspended thread can never hold
ntSuspendLock (suspension only happens under it), which is the
mutual-suspend deadlock proof.

### exit() discipline (the ExitProcess-vs-SuspendThread wedge)

- runtime.exit's NT asm branch is now a tail JMP to the Go ntExit:
  lock(&ntSuspendLock) FOREVER, ntExiting=1, ExitProcess. So no
  suspension can be mid-flight while the process dies - the wedge
  upstream exit() documents (suspender killed between SuspendThread
  and ResumeThread -> target frozen forever).
- ntKillSelf's normal-operation encoded deaths (SIGKILL, SIG_DFL
  terminate - the waitsig check's exact path) go through
  ntExitEncodedOrdered = same lock + ntExitEncoded. Crash paths
  (ntWinthrow from the VEH, raise/raiseproc's dieFromSignal exits)
  deliberately stay on the bare asm ntExitEncoded: taking runtime
  locks from an exception handler is worse than upstream's accepted
  dieFromException equivalent - same residual risk profile as
  upstream windows.
- WSAPoll-parked poller Ms and WaitOnAddress-parked Ms need nothing
  special at exit: they are parked, not suspended; ExitProcess
  terminates parked threads fine. The invariant that matters is
  "SuspendThread only ever under ntSuspendLock", which ntExit's
  forever-hold closes.

### osPreemptExtEnter/Exit (the cgocall bracket)

preempt_nonwindows.go now excludes cosmo; os_cosmo.go defines the
real pair (iswindows-gated, mirroring upstream: Enter spins on the
CAS with osyield while a preemption is in flight, Exit is a plain
store). They wrap the blocking foreign calls in ntcallSE/ntcallSE10
STRICTLY inside the entersyscall window (enter after entersyscall,
exit before exitsyscall) - exitsyscall can schedule other goroutines
on this M, which must never run with preemptExtLock held. Effect on
a blocked M (ReadFile on a console, CreateProcessW mid-image-load):
ntPreemptM fails fast (CAS) instead of suspending a thread that may
hold the loader lock. Runtime-internal g0 ntcalls (netpoll wake
sends, WSAPoll, futex waits) take NO bracket on purpose - upstream's
stdcall-vs-cgocall split; they are protected by gFromSP->g0.

### Console control -> os/signal (SetConsoleCtrlHandler)

- The callback runs on a Windows-INJECTED thread: no g, no TLS.
  ntCtrlTramp (sys_cosmo_nt_amd64.s) is therefore Go-free asm using
  only volatile registers and direct win64 calls (chunk-B alignment
  discipline): classify dwCtrlType (0/1 CTRL_C|BREAK -> SIGINT(2);
  2/5/6 CLOSE|LOGOFF|SHUTDOWN -> SIGTERM(15); else return 0), LOCK
  OR the signal bit into ntCtrlMask, SetEvent(ntCtrlEvent), then
  return 1 for the SIGINT class - or, for the SIGTERM class, park
  forever in a Sleep(INFINITE) loop: Windows kills the process the
  moment a CLOSE/LOGOFF/SHUTDOWN handler returns, and blocking
  grants Go handlers the OS grace window (upstream ctrlHandler's
  block(), ~5-20s system deadline).
- The relay: ntInitConsoleCtrl (called from mstartm0 via the new
  cosmoMstartm0 hook in proc.go + stubs_noncosmo.go stub) creates
  the auto-reset event, parks a dedicated relay M on it
  (newm(ntCtrlRelay, nil, -1) - no P, blocked in
  WaitForSingleObject on its g0, the sysmon shape), and registers
  the handler. FIRST ATTEMPT FAILED and is instructive: goenvs -
  where upstream registers ITS handler - is too early for newm,
  because allocm borrows the caller's P and m0 only acquires p0 in
  procresize at the END of schedinit (the new VEH caught it with a
  perfect traceback: acquirep(nil) in allocm). mstartm0 runs just
  after, with p0 held, and its newextram precedent already
  allocates there.
- Delivery: the relay drains ntCtrlMask (Xchg) and runs ntKillSelf
  per signal - the same kernel decision tree kill(2) uses, against
  ntSigActs: SIG_IGN drops, SIG_DFL dies encoded (0xC0DE0002 for an
  unwatched Ctrl+C - the Linux default action; deliberate delta
  from upstream, which returns 0 and lets the OS exit with
  STATUS_CONTROL_C_EXIT), an installed handler goes through the D1
  trampoline into sigtrampgo -> sighandler -> sigsend/os signal
  (Notify) or dieFromSignal. The mask+auto-reset-event pair cannot
  lose events: bits latch before SetEvent, the relay re-drains
  after each wait.
- Cost note: one standing parked thread per process. Upstream avoids
  it via compileCallback+needm (extra-M machinery the cosmo NT port
  does not have); revisit if it ever matters.

### Wine verification of console ctrl (limits documented)

- GenerateConsoleCtrlEvent is UNAVAILABLE under this headless wine:
  redirected-stdio processes have no console (error 12), and
  AllocConsole is denied (error 5 - no console host in the
  environment), so the OS-injected path cannot fire locally. CLOSE/
  LOGOFF/SHUTDOWN cannot be generated programmatically by API design
  anywhere.
- What WAS verified under wine (throwaway runtime hook driving the
  REAL registered asm callback through the win64 trampoline - the
  same CX=dwCtrlType ABI the OS dispatcher uses; hook deleted, not
  committed):
  * notify path: callback(0) -> verdict 1, SIGINT arrives at
    os/signal.Notify; callback(2) from a parked goroutine -> blocks
    forever (by design) AND SIGTERM arrives at Notify. rc 0.
  * ignore path: signal.Ignore(SIGINT) -> event dropped, process
    survives.
  * default path: unwatched CTRL_C -> process dies encoded (shell
    sees rc 2 = low byte of 0xC0DE0002, the ntKillSelf(SIGINT)
    SIG_DFL action; code-identical to the D1-wine-verified waitsig
    death path plus the new suspendLock).
  * every probe/sockstress/fizzbuzz run boots with the handler
    registered and the relay M parked - the full battery doubles as
    a regression test that the standing relay disturbs nothing.
- Untested residue for real NT: the OS's actual foreign-thread
  injection (the handler touches no TLS by construction - the same
  discipline ntsigtramp's g==nil path proved for foreign threads)
  and real conhost CTRL_C dispatch. windows-latest CI judges; the
  probe does not exercise console ctrl (backlog item for a later
  probe extension).

### Fork-shared file deltas (kept minimal)

- proc.go mstartm0: one cosmoMstartm0() call (stub for !cosmo in
  stubs_noncosmo.go - the established hook pattern).
- preempt_nonwindows.go: build tag grows && !cosmo.
- defs_cosmo_{amd64,arm64}.go: _SIGTERM = 0xf was simply MISSING
  from the signal constant block (the list jumped 0xe -> 0x10);
  nothing referenced it before the console-ctrl work.
- signal_unix.go, preempt.go, cgocall.go: untouched.

### Chunk-D2 commits

- 6c4e3c11 runtime: NT async preemption, per-M thread handles,
  console control

### What chunk E must do (CI flip) + real-NT risks

- Flip (recon section (d), restated): (1) TestRuntimeProbe's
  windows t.Skip at testdata/ape/apetest/runtimeprobe_test.go:69-74
  ("probe execution is a later wave") becomes a direct
  `cmd = exec.CommandContext(ctx, bin, mark)` - no shell, mirroring
  fizzbuzz_test.go:54-59 (unix keeps `/bin/sh bin mark`). (2) In
  .github/workflows/cosmo-ci.yml's test-windows job, the two apetest
  steps at :312-322 (windows-origin) and :332-342 (ubuntu-origin)
  set FIZZBUZZ_BIN only - add
  RUNTIMEPROBE_BIN="$GITHUB_WORKSPACE/binaries/ape-binary-<origin>/
  runtimeprobe.com" to both, mirroring the unix legs at
  :145/:168/:191. Optional redundancy rung: a direct pwsh probe run
  after the fizzbuzz acceptance (:247-298 pattern). Also update the
  stale job comment :216-219 and the workflow header :4-9.
- Known real-NT risks to expect on windows-latest, in likelihood
  order: (1) unixsock on Server 2022 - afunix.sys exists but the
  runner image's support for AF_UNIX pathname sockets must prove
  itself (the ONLY check wine could not green-light); (2) timer
  granularity - wave 1's Sleep(ms) usleep gives ~15.6ms quanta on
  real NT (wine's happens to be finer): if the probe's timer
  thresholds flake, resolve CreateWaitableTimerExW with
  CREATE_WAITABLE_TIMER_HIGH_RESOLUTION (0x2) + SetWaitableTimer
  with negative 100ns due times and give each M a highResTimer in
  minit (upstream os_windows.go:398-432, notes section 10); (3)
  console CP/VT on real conhost (SetConsoleOutputCP(CP_UTF8) is
  boot-applied but wine-with-redirect never proved a real console);
  (4) WER dialogs - preventErrorDialogs is live since D1, but real
  WER has more moving parts (WerSetFlags resolves on real NT, was
  missing on wine).

## Wave 2 chunk E (2026-07-18): CI flip — windows-latest runs the probe

End state: acceptance run 29650537288 fully green — all 8 jobs, with
test-windows running the full apetest suite (fizzbuzz battery + probe
+ format tests) against ubuntu-, windows- AND macos-origin fat APEs.
TestRuntimeProbe 0.41-0.44s per origin, every check ok including
unixsock/unixecho on real afunix.sys, preempt 180-186ms. Three
iterations were needed; both fixes were real-NT-only defects that wine
could not exhibit, which is exactly why the risk list called
windows-latest "the judge".

### The flip itself (iteration 1, cd5fb34a)

- runtimeprobe_test.go: the windows t.Skip became the fizzbuzz-shaped
  direct launch `exec.CommandContext(ctx, bin, mark)` (PE boot needs
  no shell; unix keeps /bin/sh).
- cosmo-ci.yml test-windows: RUNTIMEPROBE_BIN added to both apetest
  steps, plus a THIRD leg (download + apetest + log upload) for the
  macos-origin artifact so windows tests all three origins like the
  unix runners; job cap 30->40 (sum of per-step caps); header + job
  comments refreshed.

First real-NT verdict: 37-38/39 checks green immediately. Red:
unixsock on all three origins (`bind: operation not supported` =
EOPNOTSUPP <- WSAEOPNOTSUPP), one sigterm flake ("signal not
delivered within 5s"), and a ~5.0s stall signature: preempt "ok" at
5.0723/5.0846s in two runs (vs 190ms when healthy) - both values =
5s + normal work, i.e. rescued at EXACTLY the leftover 5s time.After
deadline pending from the preceding signal check.

### Iteration 2 (1611ed68): wrong theory, right instrument

Hypothesis "afunix needs WSA_FLAG_OVERLAPPED" (every known-working
consumer creates overlapped sockets) + SIO_UDP_CONNRESET guards on
the UDP wake socket + wake-send failure printlns + a NEVER-FAILING
diagnostic CI step that natively P/Invokes WSASocketW/bind for
AF_UNIX on the runner. Result: still red, same errors - but the
diagnostic returned gold: the runner binds AF_UNIX fine with our
EXACT creation flags (non-overlapped 0x80) and sockaddr shape, and
the printlns proved wake sends never fail. Both hypotheses dead; the
poison had to be a socket-STATE delta on our listener, and the wake
loss had to be in DELIVERY, not send.

### Iteration 3 (30f65650): both root causes, evidence-pinned

1. SO_REUSEADDR poisons afunix bind. The one state delta between the
   diagnostic's clean socket and net's listener: listenStream sets
   SOL_SOCKET/SO_REUSEADDR on every stream listener. On Windows,
   msafd ACCEPTS the setsockopt (returns 0), then afunix.sys refuses
   the subsequent bind with WSAEOPNOTSUPP. The extended diagnostic
   matrix proved it on the runner in the same green run:
       native plain (flags 0x80): bind OK
       native +SO_REUSEADDR: setsockopt SO_REUSEADDR OK
       native +SO_REUSEADDR: bind failed 10045
       native +FIONBIO: bind OK
   Fix: ntEmuSetsockopt swallows SOL_SOCKET/SO_REUSEADDR for
   AF_UNIX sockets (success, no winsock call) - which IS the Linux
   semantics: the option is accepted and has no effect on pathname
   binds (a taken path is EADDRINUSE regardless). The iteration-2
   overlapped-flag change was REVERTED as disproven; creation stays
   uniformly non-overlapped.
2. Loopback UDP drops on real NT = lost netpoller wakes. The wake
   channel was a self-connected loopback UDP socket. UDP - loopback
   included - may legally drop; wine's in-process loopback never
   does, so 4x wine probe runs and the whole sockstress battery
   could not catch it. One dropped wake byte leaves the poller
   asleep for its full WSAPoll timeout, and because mutators
   (netpollopen/arm/close) block on ntMtxset until the poll cycle
   ends, and new-earlier timers only reach the poller via
   netpollBreak, EVERYTHING queues behind the stale deadline - the
   observed 0-3 random ~5s stalls per run (sigterm's own 5s timeout
   was often both the victim and the rescue). Fix: the wake channel
   is a connected loopback TCP pair built at netpollinit (listener
   -> blocking connect -> accept -> close listener; TCP_NODELAY on
   the send end; both ends nonblocking; accepted end at slot 0).
   TCP retransmits - a wake byte cannot be lost - restoring the
   losslessness every upstream netpollBreak transport has
   (windows PostQueuedCompletionStatus, aix a pipe).

Supporting change: runtimeprobe's main wraps each check block in a
2s stopwatch - a slow block prints `slow: <label> took <d>` plus one
poller-counter sample (the wave-9 forensic counters) - so any future
latency stall names its block in CI logs without weakening a single
verdict. The green run has ZERO slow lines across all three probe
executions.

### Verification ladder for the green commit (30f65650)

linux native probe 39 ok + "ok all" rc 0 (no slow lines); wine probe
x4: sole red unixsock (wine ws2_32 AF_UNIX gap - socket() fails
EAFNOSUPPORT before bind), preempt 171-212ms, no slow lines;
sockstress 8/8 + ok all under wine AND linux (the netpoller rewrite's
stress gate); fizzbuzz wine/linux byte-identical; cosmo std builds
amd64+arm64; apetest harness green on linux; actionlint clean.
windows-latest: 3x TestRuntimeProbe PASS (0.41-0.44s), 3x "ok all",
zero slow lines, apetest ~2.1s per origin, all other jobs green.

### Chunk-E commits

- cd5fb34a ci: run runtimeprobe on windows-latest against all three
  origin binaries
- 1611ed68 runtime: fix NT afunix bind and harden the netpoller wake
  socket (theory disproven and reverted next commit; the diagnostic
  step and printlns it added are what cracked the case)
- 30f65650 runtime: lossless NT netpoll wake (TCP pair), AF_UNIX
  SO_REUSEADDR no-op

# 2026-07-19: Windows (NT) bring-up — wave 3

Wave-3 charter (from the wave-2 backlog): socketpair, sendmsg/recvmsg
+ SCM_RIGHTS fd passing, SIGPROF CPU profiling, real conhost control
events + process groups, and a windows/arm64 scoping report. Ground
rules carried over unchanged: the 2-slot ntidata/ntiat IAT contract is
never extended (every new Win32 function resolves at runtime), all new
syscalls are dispatcher cases in ntSyscallEmulate (never a second
entry path), netpollopen keeps refusing non-sockets, wine proves
nothing (windows-latest is the judge), and every item lands with
probe coverage wired into apetest's probeOkChecks.

## Wave 3 item 1 (2026-07-19): socketpair(2) over a loopback TCP pair

SYS_SOCKETPAIR=53 was ENOSYS; syscall.Socketpair (and everything above
it) now works on NT. The syscall package needed ZERO changes: its
generated socketpair wrapper issues RawSyscall6(53, ...), which routes
through the NT emulation table, so the whole feature is dispatcher +
backend.

Design (src/runtime/os_cosmo_nt_sock.go):

- ntLoopbackTCPPair(): the netpoller wake-channel recipe of wave 2,
  factored OUT of netpollinitNT into a shared allocation-free helper
  (listener -> bind 127.0.0.1:0 -> getsockname -> listen(1) ->
  client -> blocking connect -> accept -> close listener -> strip
  HANDLE inheritance on the accepted end). Both callers stay legal at
  netpoll-init time (plain ntcallE, no entersyscall, stack buffers).
  Two hardenings the inline original lacked, gained by the poller and
  socketpair alike: (1) connect-race verification - after the accept,
  getsockname(client) must equal getpeername(accepted) over
  family+port+address, or some OTHER local process won the race
  against the one-slot backlog and the "pair" halves would be talking
  to a stranger; mismatch reports WSAECONNABORTED (the sin_zero tail
  is excluded from the compare - providers do not promise it zeroed).
  (2) TCP_NODELAY on BOTH ends, not just the poller's send end
  (best-effort, as before).
- ntEmuSocketpair(): accepts AF_UNIX/AF_LOCAL + SOCK_STREAM + proto 0
  (SOCK_NONBLOCK/SOCK_CLOEXEC stripped and honored like ntEmuSocket:
  FIONBIO per end, cloexec in the fd table). Both ends alloc as kind
  ntFDSocket with sockFam=AF_UNIX and the NEW ntFDEntry bit sockPair;
  partial failure closes both sockets and releases any claimed fd.
  Everything else rides existing socket-kind machinery: read/write ->
  recv/send, close -> closesocket, fstat -> S_IFSOCK, lseek ->
  ESPIPE, netpollopenNT accepts the fds (WSAPoll readiness +
  deadlines with zero poller changes).
- Name-query synthesis: getsockname/getpeername on a sockPair fd
  never consult winsock (they would leak the backing 127.0.0.1:port
  truth); they synthesize the Linux answer for socketpair fds - the
  UNNAMED AF_UNIX sockaddr, 2-byte family only - which the syscall
  package's pre-zeroed-buffer decode turns into *SockaddrUnix with an
  empty Name, exactly like a Linux host.
- Refusals, each deliberate: SOCK_DGRAM -> EOPNOTSUPP because a
  datagram pair would have to ride loopback UDP, and wave 2 proved
  real NT legally DROPS loopback UDP datagrams (the lost-wake
  netpoller wedge; wine cannot reproduce it) - afunix.sys has no
  DGRAM either, so there is no lossless datagram transport to build
  on. Other domains -> EOPNOTSUPP (Linux refuses AF_INET socketpair
  the same way); nonzero protocol -> EPROTONOSUPPORT.
- os/exec: a pair end CANNOT cross into a child process on NT - both
  ends are born uninheritable and ntForkExec rejects ExtraFiles
  (attr.Files longer than stdio's 3 is ENOSYS, wave 2 chunk B). So
  pathname AF_UNIX sockets remain the only cross-process socket
  channel on NT; SCM_RIGHTS over those (item 2) is the fd-passing
  story, not inherited pair ends.

Discovered while wiring the probe (not in the wave-3 plan): the
net.FileConn path needs dup(2), which was also ENOSYS. FileConn wraps
an *os.File by fcntl F_DUPFD_CLOEXEC - ntFcntl answers ENOSYS - and
internal/poll then falls back to plain dup(2) (dupCloseOnExecOld).
Fix: SYS_DUP=32 dispatches to ntEmuDup, implemented for SOCKET-kind
fds only via same-process DuplicateHandle - exactly the call upstream
Go's poll.DupCloseOnExec makes on windows (msafd sockets are real
kernel file handles; the object lives until the last handle closes,
which IS dup's contract; MSDN's warning about DuplicateHandle on
sockets concerns non-IFS layered providers, not the base msafd/afunix
stacks). The dup copies the recorded socket identity (sockFam,
sockPair, unix names) into the new fd entry and clears CLOEXEC per
POSIX; nonblocking mode needs no copy (FIONBIO is socket-object
state, shared - same as Linux's shared file description). File/pipe
dup stays ENOSYS on purpose: nothing in std needs it on NT yet, and a
visible gap beats an untested path. No new Win32 imports anywhere in
this item: DuplicateHandle and every winsock entry point were already
resolved.

Probe (testdata/runtimeprobe/sockpair.go, mandatory on ALL hosts - no
skip legs; linux native, darwin via darwinSocketpair, windows via the
new emulation; names added to apetest probeOkChecks):

- socketpair: raw-fd byte transfer in both directions plus the
  unnamed-*SockaddrUnix getsockname contract on both ends (the
  regression canary for the 127.0.0.1 leak).
- sockpairpoll: os.NewFile + net.FileConn on both ends (asserts an
  unnamed *net.UnixAddr LocalAddr), a reader parked BEFORE the write
  (forcing the EAGAIN -> netpoller wait -> readiness wake round trip,
  not a buffered fast path), the reverse echo, then a
  SetReadDeadline-in-the-past read that must time out - netpollopen
  registration, WSAPoll readiness and pollDesc deadlines on pair fds.

## Wave 3 item 1 CI followup (2026-07-19): darwin needs dup(2) too

Item 1's push failed CI on test (macos-latest) only - all three origin
binaries, identically, at exactly one check:

    FAIL sockpairpoll: FileConn(end 0): file file+net sockpair0: dup:
    function not implemented

(ubuntu and test-windows legs were green.) Root cause: the same
net.FileConn dependency item 1 fixed on NT was ALSO missing on darwin.
poll.DupCloseOnExec first tries fcntl F_DUPFD_CLOEXEC; on the macOS
runner that returned EINVAL-or-ENOSYS (the two errnos DupCloseOnExec
swallows before falling back - the log cannot distinguish them), and
the fallback dupCloseOnExecOld then issued plain dup(2) = SYS_DUP(23,
arm64 numbering), which had no case in the darwin slow-path dispatcher
-> ENOSYS -> FileConn fails. Linux has the real syscall and NT got
ntEmuDup in item 1, which is why only the darwin leg went red.

Fix (syscall_cosmo_arm64.go + os_cosmo_arm64.go): dlsym Apple's dup -
a fixed-arg libc entry with dup(2)'s exact POSIX semantics (lowest
free fd, same open file description, CLOEXEC clear) - and dispatch
sysDUP=23 to it, the same shape as every other darwin slow-path call.
dupCloseOnExecOld's follow-up CloseOnExec (fcntl F_SETFD, proven
working on darwin - the pipe2 O_CLOEXEC emulation depends on it and
execchild is green) then sets the flag.

Open question, deliberately instrumented rather than guessed at: WHY
the F_DUPFD_CLOEXEC fast path errored. darwinFcntl translates the
Linux cmd 1030 to Apple's 67 and the same fcntl plumbing passes
F_GETFL/F_SETFL/F_SETFD arguments correctly (nonblocking sockets,
deadlines and exec pipes all depend on those and are green), so the
errno was swallowed before anything logged it. The sockpairpoll probe
now prints the fast path's live verdict as an ok-line detail
(dupcloexec=ok / dupcloexec=<errno>) on every host, so the next CI
run pins the actual errno without risking a verdict on it.

## Wave 3 item 2 (2026-07-19): sendmsg/recvmsg - the plain data path

SYS_SENDMSG=46 / SYS_RECVMSG=47 were ENOSYS on NT. As with
socketpair, the syscall package already had the complete msghdr
superstructure (Msghdr/Iovec/Cmsghdr types, recvmsgRaw/sendmsgN, the
generated wrappers over 46/47), so the whole feature is again
dispatcher + backend: two new cases in ntSyscallEmulate (pointer
re-typing in the dispatch expressions, per the nosplit contract) and
a new backend file src/runtime/os_cosmo_nt_msg.go. Ancillary data is
NOT in this item - see the seam note below; this is the table-stakes
data path that makes ReadMsg*/WriteMsg*-class traffic and raw
msghdr I/O work.

Design (os_cosmo_nt_msg.go):

- Layout mirrors: ntLinuxMsghdr/ntLinuxIovec are runtime-local copies
  of syscall.Msghdr/Iovec (Linux amd64: msghdr 0x38 bytes - Name @0,
  Namelen @8, Iov @16, Iovlen @24, Control @32, Controllen @40,
  Flags @48; iovec {base,len}), verified against
  ztypes_cosmo_amd64.go. Winsock's WSABUF is {u32 len, char *buf} -
  the REVERSE field order of iovec, so every call translates through
  a WSABUF array: stack-backed to 8 entries, heap above (the
  emulation layer is ordinary Go; internal/poll batches at most 1024
  iovecs). Iovec counts above Linux's UIO_MAXIOV=1024 are refused
  with the kernel's exact errno split (EMSGSIZE from sendmsg/recvmsg,
  EINVAL from readv/writev); totals clamp at 2^31-1 (winsock counts
  are 32-bit) by shortening the overflowing buffer - callers loop on
  the short transfer, POSIX-legal.
- Streams (msg_name == nil): WSASend/WSARecv - the scatter-gather
  cousins of the plain send/recv - via ntcallSE (7 args each,
  blocking-capable, last-error captured in-trampoline). WSARecv's
  flags argument is a POINTER (in/out); returned flags translate back
  to Linux (only MSG_OOB shares a value; MSG_TRUNC is raised on
  datagram truncation; winsock-only MSG_PARTIAL is dropped). WSASend/
  WSARecv report success as 0 with the count in an out-parameter,
  unlike send/recv's count-or-minus-one. MSG_* input flags reuse
  ntMsgFlags (OOB/PEEK/DONTROUTE pass, everything else EINVAL).
  Error-mapping parity with ntSockRead/ntSockWrite is exact:
  recv-side WSAESHUTDOWN = EOF (0 bytes), send-side WSAESHUTDOWN =
  EPIPE via the table, recv-side WSAEMSGSIZE = report a full buffer
  (Linux truncates datagrams silently), plus MSG_TRUNC in msg_flags,
  which the plain recvfrom path has nowhere to report.
- Datagram-style (msg_name != nil): delegate to ntEmuSendto/
  ntEmuRecvfrom so the sockaddr translation lives in exactly one
  place; multiple iovecs coalesce through an allocated buffer
  (winsock's one-buffer sendto/recvfrom). Note std's Recvmsg ALWAYS
  supplies a name buffer, so connected-stream receives take this
  delegate too: winsock's recvfrom ignores the address on connected
  streams and leaves the pre-zeroed buffer as family AF_UNSPEC = "no
  source address", exactly what syscall.Recvmsg keys on (knowing
  divergence: Linux rewrites msg_namelen to 0 there, the delegate
  reports the AF_UNSPEC record's length; no std caller reads it).
  AF_UNIX needs no datagram story - afunix.sys is SOCK_STREAM only.
- THE ANCILLARY SEAM (for the SCM_RIGHTS item): ntSendmsgControl and
  ntRecvmsgControl. Send-side: any call with a non-empty control
  buffer routes there BEFORE data moves - today it refuses
  EOPNOTSUPP, and the SCM_RIGHTS item replaces the body with cmsg
  parsing + its fd-transfer frame. Recv-side: called iff the caller
  supplied a control buffer, BEFORE the plain receive, so the frame
  MSG_PEEK can live there and take over the whole receive
  (handled=true); today it reports handled=false and the plain path
  zeroes msg_controllen - a supplied oob buffer simply comes back
  empty (oobn=0), which is exactly Linux's behavior when no ancillary
  arrives.
- ws2_32 resolution: WSASend/WSARecv were NOT among ntWinsockEnsure's
  original 19 GetProcAddress entries - the wave-2 variables named
  ntWSASendFn/ntWSARecvFn actually held classic send/recv. Those four
  are renamed to ntSockSendFn/ntSockRecvFn/ntSockSendtoFn/
  ntSockRecvfromFn (matching their ntNameSock* strings), and the real
  WSASend/WSARecv resolve as two NEW entries ntWSASendVFn/
  ntWSARecvVFn (21 syms now). Runtime GetProcAddress only - the
  2-slot ntidata/ntiat import contract is untouched.

Probe (testdata/runtimeprobe/sendmsg.go, check "sendmsg"; in
probeOkChecks): an in-process pathname AF_UNIX pair built at the
raw-fd level (listener + dial; nonblocking accept-poll with a
deadline so no host's connect semantics can deadlock the probe), then
(1) the public syscall.Sendmsg -> syscall.Recvmsg round trip where
the receive supplies an oob buffer that must come back EMPTY (oobn=0,
recvflags=0 - the no-ancillary contract), and (2) raw two-iovec
msghdrs both directions via syscall.Syscall - nothing in std issues
multi-iovec sendmsg, and this is exactly the WSABUF scatter path -
with the recv split (7) deliberately misaligned against the send
split (11) and a short-read loop that rebuilds iovecs at the current
offset. Per-host: linux native mandatory, windows mandatory via the
new emulation, macOS prints "ok sendmsg skipped (host lacks
sendmsg)" - keyed on the host triple (OS=Windows_NT -> NT, else
/proc/version -> linux, else darwin), NEVER on an error.

### readv/writev sub-commit: net.Buffers works on NT

Wave-3 scouting found internal/poll's writev.go is //go:build unix
(cosmo included), so net.Buffers consolidated writes issued
SYS_WRITEV=20 -> ENOSYS on NT and errored. Fixed on the same WSABUF
machinery: SYS_READV(19)/SYS_WRITEV(20) dispatch to ntEmuReadv/
ntEmuWritev for SOCKET-kind fds - readv is recvmsg minus the msghdr,
writev is sendmsg minus it (bad iovec counts are EINVAL here, the
kernel's readv/writev spelling, vs sendmsg's EMSGSIZE). Non-socket
fds stay ENOSYS ON PURPOSE: nothing in the standard library issues
readv/writev on NT files or pipes (poll's only writev consumer is
net's netFD; there is no readv consumer at all), and exec's stdio
pipes must stay on the blocking ReadFile/WriteFile path the
netpoller refuses to adopt - a visible gap beats an untested
vectored file path, same reasoning as the file/pipe dup refusal.

Probe: check "netbuffers", mandatory on ALL THREE hosts (linux
native, darwin dispatches Apple readv/writev, NT via the new cases;
no skip legs): raw two-iovec writev/readv over a socketpair with
misaligned splits and a short-read loop, then a net.Buffers
consolidated write through net.FileConn ends (the whole
internal/poll.Writev stack) byte-compared on the reader side. On
darwin this leg also regression-guards the item-1-followup dup(2)
emulation, since FileConn's dup fallback is exactly what failed
there.

## Wave 3 item 2b (2026-07-19): SCM_RIGHTS fd passing over afunix

The prize of item 2: sendmsg with a SOL_SOCKET/SCM_RIGHTS control
payload now transfers fds between cosmo processes on NT, over
pathname AF_UNIX SOCK_STREAM sockets. NT has no primitive for
attaching handles to socket messages, so the emulation defines a wire
frame that rides the ordinary afunix byte stream - emitted ONLY by
sendmsg calls that actually carry rights; plain sends stay unframed
and wire-compatible with write/send on the same socket. Both seam
functions in os_cosmo_nt_msg.go (ntSendmsgControl/ntRecvmsgControl)
gained their real bodies; the syscall package again needed zero
changes (Sendmsg/Recvmsg/UnixRights/ParseSocketControlMessage all
flow through the SYS_SENDMSG/SYS_RECVMSG dispatch).

Frame layout (little-endian, byte-serialized, versioned):

    off  size  field
    0    8     magic: F5 53 43 4D 52 49 47 30 - 0xF5 (improbable
               first byte: illegal UTF-8 lead) then "SCMRIG" then
               the VERSION byte '0'
    8    4     nfds (u32, capped at 64 - Linux's own SCM_MAX_FD is
               253; 64 keeps the worst-case frame ~41 KiB, under
               afunix's default socket buffer)
    12   4     sender pid (u32, diagnostic only)
    16   4     dataLen (u32): data bytes following the records
    20   4     reserved (0)
    24   ...   nfds records, then dataLen data bytes

    record: u32 kind (1=file, 2=pipe, 3=socket - wire values,
    decoupled from the internal ntFDKind enum), u32 Linux O_* flags,
    then: file/pipe -> u64 RECEIVER-relative HANDLE value;
    socket -> u16 infoLen (=628, sizeof WSAPROTOCOL_INFOW on x64) +
    the WSAPROTOCOL_INFOW blob.

SENDER-PUSH model, the load-bearing design decision: all duplication
happens at sendmsg time - WSADuplicateSocketW(s, peerPid, &info) for
sockets (no process handle needed, just the pid), and
OpenProcess(PROCESS_DUP_HANDLE=0x40, peerPid) + DuplicateHandle(self,
h, hPeer, &peerRel, 0, FALSE, DUPLICATE_SAME_ACCESS) for files and
pipes - so the sender may close its fd (or exit) the moment sendmsg
returns, which is the Linux invariant. Receiver-pull was considered
and REJECTED for exactly that reason: between sendmsg and recvmsg the
sender's handle could be closed or its value recycled, so a frame
carrying sender-relative handle values would dangle. The whole
header+records+data goes out as ONE vectored WSASend, looped to
completion on short sends (a nonblocking carrier that accepts only
part of a frame MUST be finished - the receiver consumes frames
whole; EAGAIN with zero progress returns cleanly, EAGAIN after
partial progress yields and retries).

Peer-pid discovery: the afunix.sys SIO_AF_UNIX_GETPEERPID WSAIoctl
(0x58000100, _WSAIOR(IOC_VENDOR, 256); in the SDK's afunix.h, stable
since Win10 17063 - the moral equivalent of the SO_PEERCRED pid that
afunix lacks). Answer cached on the fd entry (sockPeerPid): a
connection's peer can never change. Ioctl failure -> EOPNOTSUPP for
ancillary sends only; plain data on the same socket is unaffected.
FALLBACK PLAN if windows-latest refuses the ioctl (the one
undocumented dependency): flip to receiver-pull - frame carries
senderPid (already does, as a diagnostic); receiver OpenProcess +
DuplicateHandle-pulls; sender parks a self-duplicate per transfer to
survive early close, reaped at socket close. The frame version byte
exists precisely so that flip is a contained, detectable change.
Decision point: the first windows-latest run of the fdpass probe.

Receive path: recvmsg WITH a control buffer MSG_PEEKs 8 bytes on
AF_UNIX non-pair sockets. Short peeks that match a magic prefix
re-peek after a yield (the sender emits frames in one send, so the
rest is in flight); any non-magic byte falls through to the plain
data path (handled=false). On a match: consume the self-delimiting
frame with exact-read loops, reconstruct fds - sockets via
WSASocketW(FROM_PROTOCOL_INFO=-1,-1,-1, &info, 0,
WSA_FLAG_NO_HANDLE_INHERIT), mirroring ntEmuSocket's non-overlapped
creation shape (WSA_FLAG_OVERLAPPED must NOT appear: wave-2 sockets
are classic synchronous); files/pipes by inserting the
already-receiver-relative handle straight into the fd table with the
carried kind/flags - then synthesize the Linux SCM_RIGHTS cmsg into
the caller's control buffer and deliver the data bytes into the
iovecs. The imported socket shares the sender's underlying socket
object (blocking mode, options), exactly like a passed fd's shared
open file description on Linux; the Linux address family is read out
of the info blob's iAddressFamily (offset 76, NT numbering, 23->10).

Linux-parity corners, each verified against a live kernel this
session (test program, 2026-07-19): SCM_RIGHTS on an INET/INET6
socket is silently DROPPED and the data sent (__sock_cmsg_send:
"SCM_RIGHTS ... semantically in SOL_UNIX"); non-SOL_SOCKET cmsg
levels are silently skipped (__scm_send's continue); an unknown
SOL_SOCKET type is EINVAL; a bad payload fd is EBADF before any data
moves; receive-side fd budget is scm_max_fds = (controllen-16)/4 -
which deliberately lets CMSG_SPACE's alignment slack carry an extra
fd (a 24-byte buffer receives TWO fds, verified) - with overflow fds
closed and MSG_CTRUNC raised; reported controllen is
min(CMSG_SPACE(4n), supplied) with cmsg Len = CMSG_LEN(4n).
SCM_CREDENTIALS is EOPNOTSUPP (Linux validates credentials this
emulation cannot).

Honest limits, by design (also in the os_cosmo_nt_msg.go header):
both ends must be cosmo-Go binaries speaking the frame (a foreign
peer reads frame bytes as data; a foreign sender's raw bytes can
alias the magic with probability ~2^-64 per message boundary,
surfaced as EBADMSG/EPROTO - and a receiver that does a PLAIN read
during a frame arrival gets frame bytes as data, where Linux would
quietly discard the fds); same-user only (OpenProcess across users
needs privileges the emulation does not negotiate); SOCK_STREAM
pathname carriers only; socketpair ends refused EOPNOTSUPP as
carriers AND payload (in-process peers by construction - ExtraFiles
is ENOSYS - and their synthesized unnamed-AF_UNIX identity cannot
cross the frame honestly); dir/stdio payload fds EOPNOTSUPP (files,
pipes, and non-pair sockets pass - pipes transfer even though
same-process dup(2) on them stays ENOSYS, since DuplicateHandle
works on any kernel handle); MSG_PEEK on a frame EOPNOTSUPP; data
past the caller's iovecs is consumed and discarded with MSG_TRUNC
(the frame is a unit; Linux leaves stream tails readable). RESIDUAL:
a duplicated socket whose last sender-side handle closes before the
receiver imports the WSAPROTOCOL_INFOW is provider-dependent (msafd
keeps it importable in practice); the fdpass probe deliberately
sequences import-before-close (the parent holds every passed fd open
until the child's echo - which implies import - arrives). A frame
error mid-transfer is all-or-nothing on the receive side: created
fds are closed, remaining records' resources dropped
(import-then-closesocket releases a socket duplication reference),
and the caller sees EBADMSG/EPROTO/the mapped errno.

New Win32 resolutions, runtime GetProcAddress only (the 2-slot
ntidata/ntiat import contract is untouched): ws2_32
WSADuplicateSocketW joins ntWinsockEnsure (22 syms now); kernel32
OpenProcess joins ntResolve. WSAIoctl, WSASocketW, DuplicateHandle
and CloseHandle were already resolved. No trampoline changes:
WSAIoctl rides the existing ntcall10x shape, WSADuplicateSocketW and
OpenProcess are 3-arg, DuplicateHandle 7-arg.

Probe (testdata/runtimeprobe/fdpass.go, check "fdpass"; in
probeOkChecks): parent/child over a pathname unix socket
(RUNTIMEPROBE_CHILD=fdpass, path via RUNTIMEPROBE_FDPASS_SOCK). The
parent passes a FILE fd (known content, reopened O_RDONLY) and a
SOCKET fd (the accepted end of an in-parent TCP loopback triple,
dup'd out via File() - the item-1 socket dup) plus inline data in
ONE sendmsg; the child reads the file through the passed fd, writes
a payload into the passed socket (verified arriving on the parent's
dial side - proving the passed fd is the same underlying socket),
and echoes file-content|inline-data back over the unix conn; the
parent asserts all three arrivals and a clean wait status.
Mandatory-real on NT AND linux - the linux leg runs native
SCM_RIGHTS, which validates the probe's own logic and pins the
reference semantics the emulation must match; darwin prints "ok
fdpass skipped (host lacks sendmsg)" keyed on the host triple, never
on an error. Verified this session on the linux leg: all 44 checks
ok, "ok all", exit 0. The NT frame path is judged by windows-latest
only (wave-2 rule; wine lacks afunix entirely).

## Wave 3 item 3 (2026-07-19): CPU profiling - the profileLoop port

Before this item, pprof.StartCPUProfile on NT succeeded, set
mp.profilehz, armed NOTHING (setProcessCPUProfiler's iswindows()
early-return), and StopCPUProfile wrote a valid profile with zero
samples - a silent lie. Now the early-return calls into a port of
upstream os_windows.go's profiler (new file os_cosmo_nt_prof.go):

- ntSetProcessCPUProfiler: on first use (the profiletimer==0
  once-guard - sound because setcpuprofilerate holds prof.signalLock
  around every call) create a waitable timer and `newm(ntProfileLoop,
  nil, -1)` - a standing no-P M, the ntCtrlRelay precedent and
  upstream's exact `newm(profileLoop, nil, -1)`. A real M means g0 +
  TLS + legal ntcall use; the foreign-thread trampoline prohibition
  never applies. Timer creation degrades: CreateWaitableTimerExW with
  CREATE_WAITABLE_TIMER_HIGH_RESOLUTION=0x2 + TIMER_ALL_ACCESS ->
  same without the flag -> CreateWaitableTimerW(0,0,0). If all three
  fail the timer stays 0, the M is never started, and arming is
  skipped - profiling degrades to the pre-item-3 sampleless behavior
  instead of hot-looping WaitForSingleObject on handle 0.
- ntSetThreadCPUProfiler: upstream's due/period math verbatim - hz>0:
  ms=1000/hz (min 1), due=-10000*ms (negative = relative, 100ns
  units), period ms; hz<=0: due=minInt64 (a one-shot ~29k years out),
  period 0 - upstream's disarm shape. The shared `mp.profilehz = hz`
  store in setThreadCPUProfiler (os_cosmo.go) stays for all hosts and
  runs AFTER the NT arm, exactly upstream's order.
- ntProfileLoop: best-effort SetThreadPriority(GetCurrentThread=-2,
  THREAD_PRIORITY_HIGHEST=2) so ticks land even when every P spins;
  forever { plain ntcall WaitForSingleObject(timer, INFINITE) - the
  relay-M idiom, never ntcallSE (this M has no P and no syscall
  bracketing); walk allm via atomic.Loadp + alllink }. Per M: skip
  self; under mp.threadLock skip mp.thread==0 / mp.profilehz==0 /
  mp.blocked, else DuplicateHandle an own reference; release
  threadLock; THEN suspend. mp.blocked is real on cosmo: lock_futex.go
  builds for (cosmo && !arm64) and sets it around futexsleep, so
  note-parked Ms (idle workers, timer thread, scavenger) are skipped
  exactly like upstream. Ms parked inside WSAPoll (the netpoller) are
  NOT note-blocked, so they ARE sampled and attribute to external
  code - upstream windows' own behavior, harmless.
- ntProfileM: 16-aligned full-size ntContext (the ntPreemptM
  alignment idiom), ContextFlags=_NT_CONTEXT_CONTROL (0x100001 =
  CONTEXT_AMD64|CONTEXT_CONTROL, the same constant preemption uses -
  and byte-identical to upstream windows.CONTEXT_CONTROL; sigprof
  consumes only pc+sp, and nothing is written back), GetThreadContext,
  gp := ntGFromSP(mp, rsp), then the DIRECT `sigprof(rip, rsp, 0, gp,
  mp)` - upstream profilem's shape, lr=0 on amd64. sigprof is
  host-independent and takes no signal number, so "SIGPROF parity"
  involves no signal numbering at all; the setitimer asm and the
  SYS_SETITIMER=38 ENOSYS dispatch are untouched and stay unreachable
  on NT.

THE deliberate divergence from upstream: upstream profileLoop
suspends lock-free; here SuspendThread happens only under
ntSuspendLock, held across SuspendThread -> GetThreadContext (what
completes a suspension) -> sigprof -> ResumeThread. The wave-2
contract is load-bearing: ntExit takes ntSuspendLock FOREVER before
ExitProcess, so a profile tick can never be mid-suspension while the
process dies (upstream guards its exit only against preemption's
suspensions; ours covers the profiler by construction). At a 10ms
tick the serialization against preemption's suspensions costs
nothing. Deliberately NOT ported: mp.preemptExtLock (upstream's
profiler ignores it too - the profiler only READS context, so
suspending a thread inside win64 code is benign and the sample lands
in external code) and per-M highResTimer (upstream's usleep
machinery, unrelated).

Deadlock audit (the pre-commit gate for this item):

- Lock ORDER equals ntPreemptM's exactly: threadLock(target) taken
  and RELEASED before ntSuspendLock; no threadLock is ever taken
  under ntSuspendLock, and this M holds no lock a suspended thread
  could want. A target inside ntMinitThread/ntUnminitThread holding
  its own threadLock simply delays the profiler BEFORE any
  suspension. ntPreemptM and ntProfileLoop serialize on
  ntSuspendLock, so neither ever suspends the other mid-window.
- The suspended thread may hold ARBITRARY locks, so the window runs
  only what a unix SIGPROF handler runs interrupting arbitrary code:
  sigprof's callee set. Audited: cpuprof.add / addNonGo carry the
  explicit contract "called from signal handlers ... cannot allocate
  memory or acquire locks that might be held at the time of the
  signal" (cpuprof.go) and take only prof.signalLock, a CAS spinlock
  (osyield spin; osyield's NT leg is Sleep(0), lock-free);
  profBuf.write is the lock-free ring + notewakeup -> futexwakeup ->
  WakeByAddressSingle, no runtime mutex anywhere; traceCPUSample is
  signal-context-rated upstream. sigprof's mallocing++ trap enforces
  the no-alloc part at runtime. No print/throw happens while
  anything is suspended (the DuplicateHandle print+throw sits before
  the suspension, upstream's own placement; a GetThreadContext
  failure silently skips the sample).
- The one lock sigprof CAN contend on, prof.signalLock, cannot be
  held by the suspended thread: its only other taker,
  setcpuprofilerate, stores its caller's mp.profilehz = 0 (via
  setThreadCPUProfiler) BEFORE acquiring it, and the loop RE-CHECKS
  `mp.profilehz != 0 && !mp.blocked` AFTER SuspendThread (upstream's
  re-check, ported). x86-TSO + the kernel suspension barrier order
  the plain profilehz store before any post-suspend load, so a
  thread suspended inside the signalLock window is always seen with
  profilehz==0 and skipped. (Upstream windows makes that store
  atomic as belt-and-braces; the cosmo store is a plain aligned
  int32 - same visibility argument, NT is amd64-only.)

New Win32 resolutions, runtime GetProcAddress only (IAT untouched):
kernel32 CreateWaitableTimerW + SetWaitableTimer (hard, crash-poke),
CreateWaitableTimerExW + SetThreadPriority (optional-graceful - the
Ex creator's HIGH_RESOLUTION flag needs Win10 1803, old wine lacks
the export; priority is best-effort). WaitForSingleObject,
SuspendThread, ResumeThread, GetThreadContext, DuplicateHandle,
CloseHandle were already resolved for exec/preemption. Everything
fits the existing ntcall/ntcall7 shapes; ntcallArgs never widens.
Timer granularity: without HIGH_RESOLUTION the 10ms period coalesces
to the 15.6ms scheduler quantum (~64Hz effective) - accepted; the
probe asserts >=1 sample, never a rate.

Probe (testdata/runtimeprobe/cpuprof.go, check "cpuprof"; in
probeOkChecks between preempt and waitsig): StartCPUProfile into a
buffer, 1.2s of per-P spin goroutines (the preempt recipe; under the
probe's 2s slow-block stopwatch), StopCPUProfile, then assert the
profile is non-empty, gunzips, and a bounded ~40-line protobuf
walker counts top-level field-2 (Sample) records >= 1 - exactly the
zero-samples silent failure this item kills. Mandatory-real on NT
(the deliverable) AND linux (native setitimer SIGPROF - regression-
guards the shared pprof surface and validates the walker against a
known-good producer); darwin prints "ok cpuprof skipped (no SIGPROF
on this host)" keyed on the host triple (setitimer is genuinely not
dispatched there - the wave-2 backlog gap stands). NT truth is
windows-latest (wave-2 rule).

## Wave 3 item 4 (2026-07-19): real conhost control events - SIGQUIT/SIGHUP mapping, process groups, kill(-pgid)

Wave 2's console-ctrl chain (asm handler on the conhost-injected
foreign thread -> ntCtrlMask -> relay M -> ntKillSelf) had two gaps:
the event mapping was upstream windows Go's (BREAK -> SIGINT, CLOSE ->
SIGTERM), and NOTHING in CI had ever fired the OS-injected leg - every
signal check reached the relay via self-kill. Item 4 fixes the mapping,
adds process groups + kill(-pgid), and lands the probe that finally
drives the injected-thread path end to end on windows-latest.

### The mapping change (a deliberate upstream divergence)

ntCtrlTramp (sys_cosmo_nt_amd64.s) now classifies:

    CTRL_C_EVENT(0)        -> SIGINT(2)   return TRUE
    CTRL_BREAK_EVENT(1)    -> SIGQUIT(3)  return TRUE   [was SIGINT]
    CTRL_CLOSE_EVENT(2)    -> SIGHUP(1)   block forever [was SIGTERM]
    CTRL_LOGOFF_EVENT(5)   -> SIGTERM(15) block forever
    CTRL_SHUTDOWN_EVENT(6) -> SIGTERM(15) block forever
    anything else          -> return FALSE (next handler)

Upstream Go maps BREAK -> SIGINT and CLOSE -> SIGTERM
(os_windows.go ctrlHandler); the divergence is for unix parity:
Ctrl-Break is the console's second chord and SIGQUIT is unix's second
chord - with the fork sigtable's SIGQUIT {_SigNotify+_SigThrow},
Ctrl-Break against a WEDGED cosmo process now produces the classic
unix goroutine-dump-and-die, which upstream windows users simply do
not have; and closing the console window is the moral hangup, so
CLOSE -> SIGHUP {_SigNotify+_SigKill} (unwatched -> dieFromSignal ->
encoded 0xC0DE0001 death). Handler mechanics are UNCHANGED: foreign
thread, Go-free, direct win64 CALLs only (never ntcall6/10 - the
load-bearing wave-2 prohibition), LOCK ORL a mask bit +
SetEvent(ntCtrlEvent) wake, return TRUE for the keyboard chords,
Sleep(INFINITE) block for the lifetime events (Windows kills the
process the moment a CLOSE/LOGOFF/SHUTDOWN handler returns - blocking
buys the OS grace window). The mask is now 4 bits (1<<1|1<<2|1<<3|
1<<15) and ntCtrlRelay drains in order SIGINT, SIGQUIT, SIGHUP,
SIGTERM (chords before lifetime events); semantics keep falling out
of ntSigActs - watched -> Notify, unwatched -> the sigtable default.

### Process groups: Setpgid -> CREATE_NEW_PROCESS_GROUP

ntForkExec's SysProcAttr rejection carves out exactly `Setpgid &&
Pgid == 0` ("make the child its own group leader"): it maps 1:1 onto
CreateProcessW's CREATE_NEW_PROCESS_GROUP (0x200), threaded through a
new `flags uint32` on cosmo.WindowsFns.Spawn (bit
cosmo.SpawnNewProcessGroup) into ntSpawn's dwCreationFlags. `Pgid !=
0` ("join an EXISTING group") stays ENOSYS - NT cannot place a new
process into another process's group. The chunk-B spawn table entry
records `pgleader`; the new group's id is the child's pid.

kill(pid < -1) (ntEmuKillGroup, os_cosmo_nt_sig.go): pgid := -pid
must be a pgleader entry in OUR spawn table - everything else is
ESRCH, mirroring the positive arm's own-children-only rule (pid == 0
and pid == -1 keep their pre-wave-3 ESRCH: this process is not a
group we created, and NT has no broadcast). Then:

  - SIGQUIT -> GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT=1, pgid):
    THE reliably deliverable group event (upstream Go's TestCtrlBreak
    is built on this exact pairing); in a cosmo child the handler
    maps it back to SIGQUIT - a Linux-shaped round trip.
  - SIGINT -> GenerateConsoleCtrlEvent(CTRL_C_EVENT=0, pgid), best
    effort with a documented NT caveat: CREATE_NEW_PROCESS_GROUP
    children start with Ctrl-C DISABLED until they opt back in via
    SetConsoleCtrlHandler(NULL, FALSE), so delivery may silently
    no-op. Upstream windows Go has the identical hole; a reliable
    group chord is SIGQUIT.
  - sig 0 -> existence probe.
  - any other sig -> TerminateProcess(leader, 0xC0DE0000|sig):
    group-kill degrades to leader-kill (no NT API delivers arbitrary
    signals group-wide; the leader is the group's one member we know
    of), same best-effort result discipline as the positive arm.
  - GenerateConsoleCtrlEvent failure (e.g. no console) -> the mapped
    errno from the trampoline-captured last error.

The PARENT needs no SetConsoleCtrlHandler(NULL, TRUE) shield (the
brief's open question, resolved): GenerateConsoleCtrlEvent targeted
at the CHILD's group id never reaches the parent's group, and the
emulation never emits group-0 broadcasts (pid == 0/-1 are ESRCH by
construction), so the classic self-Ctrl-C-while-signaling-the-child
footgun cannot occur.

New Win32 resolution: kernel32 GenerateConsoleCtrlEvent (hard
k32sym). TerminateProcess was already resolved (chunk D).
ntidata/ntiat untouched; everything fits ntcallE; ntcallArgs never
widens.

### Probe (ctrlbreak) - first CI coverage of the OS-injected handler

testdata/runtimeprobe/ctrlbreak.go, check "ctrlbreak", between
cpuprof and waitsig (waitsig stays last). Child mode
RUNTIMEPROBE_CHILD=ctrlwait: Notify(SIGQUIT), print "ctrl-ready"
(only AFTER Notify returned - the install race is closed by
ordering), await delivery <= 20s, print "ctrl-got", exit 0 (timeout:
"ctrl-timeout", exit 1). Parent: selfCommand spawn with
SysProcAttr{Setpgid: true} + stdout pipe, handshake on "ctrl-ready",
syscall.Kill(-child.Pid, SIGQUIT), handshake on "ctrl-got", clean
wait. On NT the delivery chain is CREATE_NEW_PROCESS_GROUP spawn ->
GenerateConsoleCtrlEvent(CTRL_BREAK) -> conhost injects a foreign
handler thread in the CHILD -> asm ntCtrlTramp -> mask -> relay M ->
ntKillSelf -> sigtrampgo -> Notify: every self-kill check bypassed
the first three links; this is the one that proves them. On Linux
the SAME child/parent code runs over native setpgid + kernel group
kill - which validates the probe's own logic against reference
semantics. Darwin dispatches setpgid/kill and negative-pid
passthrough is expected to work: attempt-first, and ONLY a darwin
spawn or group-kill syscall error prints "ok ctrlbreak skipped
(darwin: <err>)" - NT/Linux failures (and delivery failures on any
host) stay FAIL.

Risk, recorded before the CI verdict: if windows-latest test
processes turn out to have no attached console (a pseudo-console/
detached service arrangement), GenerateConsoleCtrlEvent fails and
the probe FAILs loudly - the wave-2 CP_UTF8 console boot calls
succeeding on every run says they DO have one, so this is not
expected. The fallback, to be taken only on real CI evidence, is
demoting ctrlbreak to a windows-skip that prints the diagnostic.

### Documented, not asserted (headless CI cannot generate these)

A real keyboard Ctrl-C/Ctrl-Break on an interactive conhost;
CTRL_CLOSE (no API can synthesize a console close - the SIGHUP leg
is exercised only by a human closing the window); CTRL_LOGOFF /
CTRL_SHUTDOWN; and group-targeted CTRL_C delivery (the new-group
Ctrl-C-disabled default above makes it undeliverable to children
that never opt back in). The handler legs are live; their CI truth
remains the mapping's unit structure plus the CTRL_BREAK path the
probe drives.

## Wave-4 charter: windows/arm64 (2026-07-19, scoping only - no code)

Wave 3 closes the amd64 NT backlog it chartered; the platform item
left standing from the wave-2 list is Windows on ARM. This section
is the wave-4 charter: the container constraint, four options with a
recommendation, and the bring-up notes a wave-4 session starts from.
It is a scoping report - no code changed for it, and nothing here
touches cmd/link; the PE facts cite the wave-1 contracts as they
stand today.

### The container constraint: one PE, one Machine field

The APE carries exactly ONE PE header, at e_lfanew=0x80 with budget
[0x80,0x7FE] (wave-1 design, above): PE32+, Machine=0x8664,
ImageBase 0x100000000, entry _rt0_cosmo_nt, sections mapping the
cosmo AMD64 image at file offset 0x10000, imports = the 2-slot
ntidata/ntiat contract shared between rt0_cosmo_nt_amd64.s and
cmd/link's ape.go (drift = loud link failure by design:
apePrepareNTBoot cross-checks strings and symbol sizes). A PE has a
single Machine field and e_lfanew is a single field, so a
native-arm64 NT boot cannot ride this header. The options:

- (a) Run the EXISTING amd64 image under Windows-on-ARM's x86-64
  emulation. Zero toolchain work - the fat APE shipped today is the
  test article. The open question is emulation fidelity, and it is
  concrete; the unknowns to verify are exactly the fork's
  load-bearing NT mechanisms: gs:0x28 TLS (the TEB
  ArbitraryUserPointer read compiled into every stack-bound check),
  VEH delivery (sigpanic/recover), WSAPoll semantics (the
  netpoller), SuspendThread + Get/SetThreadContext on emulated
  threads (async preemption AND the wave-3 profiler), the
  KUSER_SHARED_DATA clock reads at 0x7ffe0000 (do emulated
  processes see the same page semantics), and
  GenerateConsoleCtrlEvent handler injection (the wave-3 ctrlbreak
  chain). One CI experiment answers all of it.
- (b) ARM64X / ARM64EC hybrid PE - Microsoft's designed "one
  binary, both machines" mechanism. Demands ARM64EC codegen
  (x64-compatible calling convention, dispatch thunks, load-config
  metadata tables) that the Go toolchain does not possess, plus
  deep ape.go surgery. Out of realistic reach; not chartered.
- (c) A second PE header - impossible. e_lfanew is singular; no
  loader honors a second COFF header.
- (d) A separate thin windows/arm64 artifact - the thin-build
  machinery exists, but this abandons the APE premise of one
  shipped file; a distribution decision, not a runtime one.

RECOMMENDATION: wave 4 opens with (a) as an experiment leg - run
the existing amd64 fat APE fizzbuzz + runtimeprobe (the full
46-check gauntlet) on a windows-11-arm runner and record the
verdict in this file BEFORE any native work is chartered. If
emulation is clean, WoA support is effectively free and a native
port becomes a performance question; only if emulation is
unacceptable does the native ladder below get chartered.

### Native bring-up notes (if ever pursued)

Collected now while the amd64 design is fresh; none of this is
scheduled work.

- TEB/TLS: win-arm64 keeps the TEB pointer in x18, the reserved
  platform register - and Go's arm64 port already reserves it
  (REGPR = REG_R18, "ARM64 platform register, unused in the Go
  toolchain", cmd/internal/obj/arm64/a.out.go), so the codegen
  needs no register-diet change. The TEB ArbitraryUserPointer slot
  sits at x18+0x28: the amd64 gs:0x28 trick ports as an
  x18-relative load at the SAME offset, and the wide-TEB
  stack-bounds policy (ntSetTEBStackBounds) carries over.
- Trampoline: SysV-arm64 and win-arm64 integer argument passing
  coincide on x0-x7, so the ntcall analog is far thinner than
  amd64's SysV->win64 register shuffle; stack alignment is 16
  bytes on both sides. (Variadics differ on win-arm64, but the
  runtime's fixed-arg Win32 surface never hits that.)
- Exceptions/preemption/profiling: AddVectoredExceptionHandler,
  SuspendThread, Get/SetThreadContext are machine-independent
  APIs. CONTEXT_ARM64 (ContextFlags base 0x400000; X0..X28, Fp,
  Lr, Sp, Pc, V regs) replaces ntContext, and preemption injection
  is upstream windows/arm64's shape - Lr := resume PC, Pc :=
  asyncPreempt - instead of the amd64 fake-CALL stack push. The
  wave-3 profiler is the same allm walk with the arm64 context
  flags, still under ntSuspendLock.
- Syscall dispatcher: os_cosmo_nt_sys.go is ordinary Go and ports
  nearly verbatim; only the dispatch numbers change, to
  Linux-ARM64's (openat=56 etc. - src/syscall/
  zsysnum_cosmo_arm64.go already maintains the arm64 set, and the
  darwin arm64 dispatcher in internal/runtime/syscall/cosmo/
  syscall_cosmo_arm64.go is the in-tree precedent for exactly this
  renumbering). The fd table, path layer, spawn machinery, winsock
  layer and WSAPoll netpoller are syscall-number-agnostic and
  carry over unchanged.

### CI reality

GitHub hosts public windows-11-arm runners (GA 2025). Availability
under this repo's plan/visibility must be checked at wave-4 start
(a one-line workflow probe); the experiment leg then needs no new
build machinery - the existing ubuntu-built fat APE artifact is the
input, and fizzbuzz + apetest's runtimeprobe battery is the
pass/fail instrument.

### Wave-4 experiment verdict: WoA x86-64 emulation — FAIL-to-boot (2026-07-21)

The option-(a) experiment ran; the verdict is FAIL-to-boot. The APE
BOOTS under Windows-on-ARM's x86-64 emulation - the PE handoff, the
NT personality, VEH crash delivery, and the 0xC0DE0000|sig
encoded-signal-death exit protocol are all demonstrably live under
the emulator - but 100% of launches die at one deterministic
pre-main READ SIGSEGV during early NT bring-up, before main and
before any program output. 0 execution tests pass. Option (a) does
NOT deliver WoA support with the fork as shipped.

Setup: a test-woa job (added 6ab873d2) mirrored the windows-latest
apetest leg - same three origin artifacts, same AF_UNIX probe and
fizzbuzz pre-steps, same apetest gauntlet - on windows-11-arm, with
the windows-latest per-step timeouts doubled for emulation overhead
and continue-on-error while under evaluation. The job is retired in
the same change that records this verdict; to revive it,
cherry-pick/restore it from 6ab873d2. The charter's
runner-availability question is answered: on this public repo,
GitHub-hosted windows-11-arm runners scheduled in ~3-4s on both
attempts (image windows-11-arm64 20260714.109.1).

Evidence: run
https://github.com/wow-look-at-my/gosmopolitan/actions/runs/29860644973
attempt 1 (job 88737395780) and attempt 2 (job 88739890277 - a
rerun on a DIFFERENT VM, against artifacts confirmed identical by
sha256), plus the test-log-windows-11-arm-<origin>-origin artifacts
(3-day retention). Both attempts failed with an identical
signature. Every APE launch - ~270 processes across the two boots,
ubuntu-, macos-, and windows-origin binaries alike - dies in
~0.08s with zero stdout: Exception 0xc0000005 with
ExceptionInformation[0]=0 (READ) at fault address 0x2000c9000,
exit status 0xC0DE000B (0xC0DE0000|11 = SIGSEGV). The fault
address is CONSTANT across all ~270 launches and both boots, and
it sits inside the cosmo image's fixed 0x200000000 span: the
faulting read targets the APE image mapping, not randomized OS
state. RIP differs numerically across boots (attempt 1
0x7ff81e62b518, attempt 2 0x7ffc5a14b518) but is the SAME
instruction - low 16 bits 0xb518 identical, delta 0x43bb20000
64KB-aligned, and all 5 system-module stack frames slid rigidly by
their per-module deltas, i.e. per-boot ASLR only - with RIP in
non-Go system-module code, on the boot stack, on g0. The register
file is structurally constant: rcx=r12 = a 64KB-granular early
allocation base in ~0xcb0000-0xdf0000 (the only varying value),
rax=rcx+0x338, r10=rcx+0x440, rdx=2, r8=0x200, r9=rdi=0x210,
rbx=0x21, rsi=r13=r15=0, r14=0x200, rsp=0xbffa80 in stack
[0x0,0xbffec0), gp=0x1001a8780. Read together: the fault fires
inside or beneath an early NT call made during runtime bring-up,
before main. apetest per origin: 81 PASS (every structural/format
test) / 45 FAIL (every test that execs an APE, all `exit status
0xc0de000b` with the same SIGSEGV stderr) / 1 SKIP
(TestDebugSidecars - CI artifacts ship no sidecars); the 45-name
fail set is set-identical across the three origins AND across
attempts. runtimeprobe never started - 0 checks ran.

What WORKS under emulation is worth recording: the whole boot
chain up to the fault (PE header accepted, entry stub, NT
personality up); VEH-based crash reporting with a full
register/stack dump; the wait-status encoding observed end to end
by the harness (exit 0xC0DE000B decoded as a SIGSEGV death);
console/stderr plumbing carrying the crash report out; the AF_UNIX
probe at full parity with x64 windows-latest (afunix.sys works,
including the known SO_REUSEADDR-bind-10045 quirk, byte-identical
on both WoA VMs); and instant runner scheduling. The control is
decisive: the SAME binaries (by sha256) passed the full gauntlet
on x64 windows-latest in this same run - the delta is the
emulation layer, not the binaries.

Follow-on, per the charter's decision rule: emulation is not
clean, so the native windows/arm64 bring-up gains urgency and the
native notes above stand unchanged. One caveat before chartering
the full native ladder: the failure is a single deterministic
instruction/address pair, so ONE bounded root-cause investigation
is worth spending first - WinDbg on a WoA machine stopped at the
first APE instruction, or narrowing which early NT/loader call
touches 0x2000c9000 (candidates: xtajit64 reading the image span
during translation vs an ntdll routine walking our PE mappings).
If that one fault turns out to be fixable fork-side, option (a)
may still unlock. Honest scope note: all evidence here is CI-only;
no interactive WoA debugger was available, so the faulting module
was never named.

Job fate: job-level continue-on-error keeps the RUN green, but the
job's own check run still concludes failure, and the org's
external all-builds aggregator counts job-level conclusions -
observed "1/9 builds failed: test-woa" on 6ab873d2. An
expected-red experiment leg therefore cannot live on any branch
that must merge; that is why the job is removed outright rather
than kept.

# 2026-07-19: size pass 3c — cross-arch dedup + macOS loader-side decompression: measured, both DROPPED

Two candidate fat-APE size levers were taken to full measurement (at
HEAD ef935b3c) and dropped with no code change, no branch, no PR.
Recording the numbers and the layout design facts here so the dead
ends stay dead and future layout work starts from the constraints.

Lever 1 — cross-arch dedup via shared/overlapping PT_LOAD file
extents: DROPPED. The read-only identical ceiling is ~921 KB on a
stdlib-heavy webserver (reconciles pass 1's 2.17 MB figure: that was
unstripped — strip-by-default already removed the symtab/strtab/DWARF
share). The ONLY contiguous link-time-alignable run is the
time/tzdata zip (427,523 B, same order both arches, inside
go:string.*); funcnametab interiors drift (arch-specific insertions,
nfunc 6676 vs 6527) and pctab/functab/funcdata are arch-different
(minLC 1 vs 4). Alignment kills most of the rest: 4K congruence
breaks ARM64 macOS (the embedded loader hard-refuses congruence not
mod 16384), 16K drops 64K-page arm64 Linux kernels (supported today),
and fully-safe 64K captures 323-358 KB net in ONE extent (six 64K
pages of tzdata) — fizzbuzz and runtimeprobe capture zero. Wire
benefit under `zstd -19 --long=27` is ZERO (simulated dedup came out
1,334 B WORSE) — dedup is on-disk-only, 2.5-3.0% of the fat file,
tzdata-importing binaries only. Only proportionate follow-up if ever
wanted: special-case a single shared 64K-aligned extent for the
tzdata zipdata symbol — same 323-358 KB, minimal machinery.

Lever 2 — storing the arm64 image compressed and decompressing in the
macOS loader: DROPPED on the kill criterion. Every consumer of the
arm64 image reads raw bytes at absolute file offsets: Linux arm64
self-assimilation demand-pages in place, an installed/cached `ape`
loader file-mmaps PT_LOADs, and the m1 loader file-mmaps non-exec
segments and preads only PF_X spans into anon memory. Compressed
storage therefore necessarily costs a supported boot path — and
storing both forms would GROW the file ~2.4 MB. Upstream apelink
behaves the same way (Pwrites raw PT_LOAD spans; gzips only ~12KB
loader binaries and zip assets, never machine-code payloads).
Teaching Linux arm64 to boot via a loader would regress
zero-dependency boot (noexec /tmp, read-only rootfs), and the shell
header budget is 2047/2048 bytes. Wire size is already solved by
distribution-side zstd.

Durable ape.go/boot design facts for any future layout work:
transplantPEHeader requires the amd64 image at 0x10000 with verbatim
absolute section raw pointers; shiftPOffsets applies a uniform shift
(per-phdr rewriting would break makeMachoHeader /
makeEmbeddedElfHeader assumptions); the m1 loader caps e_phnum at 18
(1024-byte phdr buffer) and enforces 16 KiB congruence; cosmo/arm64
links with p_align 0x4000; the merged phdr table has ~3.6 KB of
in-place growth room (dead section-header bytes before the note);
GOCOSMOSTRIP=0's byte-for-byte reproduction promise means any dedup
could only apply to the default stripped path; and Linux-arm64
self-assimilation has NO CI execution coverage (no arm64 Linux
runner) — treat arm64-image layout changes as under-tested.

# 2026-07-20: exec.LookPath on NT — unix PATH rules against a Windows PATH

> Reconciliation note (2026-07-21): this entry records the fix as
> developed on its own branch (#64). A concurrently developed fix for
> the same bug (#63) merged first, and the reconciled #64 merge kept
> #63's fuller lp_cosmo.go — so three statements below do not
> describe the merged tree. In the code as merged: the implicit-CWD
> probe (NoDefaultCurrentDirectoryInExePath + the full ErrDot/
> SameFile machinery) IS ported; Command/Start's lookExtensions call
> sites do NOT stay `runtime.GOOS == "windows"`-gated — they gate on
> a per-lp-file lookExtensionsEnabled() (true on NT hosts), so
> explicit paths get PATHEXT resolution there too; and the shipped
> probe is testdata/runtimeprobe/lookpath.go's self-copy dummy
> (exec.LookPath AND exec.Command, the resolved child actually run,
> plus a conditional LookPath("go") repro) — the lpcanary flavor
> below, which the wine A/B exercised, was superseded in the merge.
> What survives from this branch: the lp_unix.go constraint comment
> and the flag-shaped argv contract (probeWantArgs + the apetest
> tail). The red-then-green commit-split CI pin below was mooted on
> master (the fix landed minutes earlier via #63). The merged
> implementation's full record is the "2026-07-20: NT —
> exec.LookPath never worked on Windows hosts" section at the end of
> this file.

## Symptom (consumer regression, initially misdiagnosed as argv loss)

go-toolchain's smoke-windows step broke deterministically once the
published fat APE's windows slot became the cosmo payload on the NT
personality (the native GOOS=windows payload was removed 2026-07-18):
`go-toolchain.exe version` worked, `go-toolchain.exe --help` printed
no help and exited 1 with "go-bootstrap: go not in PATH" + "go
bootstrap: go not found in PATH and cannot determine version from
go.mod" (go-toolchain run 29738066073, both attempts byte-identical).
The failure was first read as "flag-shaped args are lost before cobra
parses" - `--help` apparently ignored, positional `version` surviving.

## Root cause: not argv — LookPath

The argv path is intact. A local wine A/B (an os.Args dump built with
the current toolchain) delivers `--help`, `-x`, `--key=value`, and
positionals byte-exact through GetCommandLineW -> ntCommandLineToArgv,
and the last GREEN smoke-windows run (go-toolchain 29666827531, Jul
19) shows the full help printing on windows-latest - built against
gosmopolitan release v10, which PREDATES the NT bring-up entirely: its
windows slot still booted the embedded native GOOS=windows payload,
i.e. upstream lp_windows.go. What actually differs on the cosmo NT
personality: go-toolchain's root PersistentPreRunE (which cobra runs
before help rendering) calls its go-bootstrap, whose
exec.LookPath("go") under GOOS=cosmo was lp_unix.go on EVERY host -
and unix rules against a real NT PATH
("C:\hostedtoolcache\windows\go\1.24.13\x64\bin;...") split entries at
the drive colon, probe extensionless names, and demand +x: they can
never succeed. LookPath fails, the go.mod fallback fails (empty smoke
cwd), the process exits 1 before help. `version` survives only because
go-toolchain exempts it from that hook (skipCache), not because
positionals fare differently. Reproduced byte-for-byte on LINUX by
running the same APE with go removed from PATH in an empty dir - the
bug is host-semantics, not windows execution.

## Fix: run-time host dispatch in os/exec (lp_cosmo.go)

lp_unix.go is now `unix && !cosmo`; the new lp_cosmo.go picks PATH
semantics at run time via the NT emulation hook (cosmo.Windows(), the
same dispatch package syscall uses): unix hosts keep lp_unix.go's
rules verbatim, NT hosts get lp_windows.go's PATHEXT search ported
onto the cosmo surface - PATH/PATHEXT read case-insensitively (NT env
names are case-folded; cosmo's unix-shaped Getenv is not), ";"-list
splitting/joining/absoluteness as local helpers (filepath here is
compiled with unix rules), executability = exists && !dir (no mode
bits on NT), drive-shaped candidates stat fine through the path
layer's passthrough. The implicit CWD probe (ErrDot +
NoDefaultCurrentDirectoryInExePath machinery) is deliberately not
ported - not searching the CWD is the end state that machinery
exists to reach. Command/Start's lookExtensions call sites stay
runtime.GOOS == "windows"-gated (unreachable under cosmo): bare names
go through the fixed LookPath; explicit paths are used as spelled.

## Regression tests (testdata/runtimeprobe + apetest)

New probe check "lookpath": plant a canary executable in a fresh
directory, point PATH at it, require exec.LookPath to resolve the
bare name to the planted file (os.SameFile). On NT the planted PATH
entry is the DRIVE-SHAPED spelling of the temp dir (recovered via the
checkWd Chdir/Getwd alias idiom) and the canary is "lpcanary.exe" -
reproducing the consumer failure exactly; on unix hosts it is the
classic extensionless/+x/":" lookup. The probe's argv contract also
grew a fixed flag-shaped tail (--help -x --key=value trailing-arg)
asserted byte-intact, pinning the originally-suspected (and
exonerated) GetCommandLineW parse for '-'-tokens. apetest passes the
tail and asserts the new check on all three legs; only the windows
leg exercises the NT personality.

## Verification

Wine A/B (wine reproduces this argv/path surface faithfully; real-NT
truth stays CI): probe built with UNFIXED master (buildhost release
r469) fails exactly one NEW check - `FAIL lookpath: exec.LookPath
("lpcanary") with PATH="C:\\users\\root\\Temp\\lookpath...": not
found` - with `ok args [... "--help" ...]` proving argv intact; the
same probe built with the fixed toolchain prints `ok lookpath
C:\users\root\Temp\lookpath...\lpcanary.exe`. Linux personality: "ok
all" both before and after the fix (unix flavor unchanged). Local:
make.bash, GOOS=linux `go test os/exec` green, `GOOS=cosmo go vet
os/exec` clean, full apetest suite green against the fixed fat
fizzbuzz + runtimeprobe. The commit split makes CI prove the pin:
the test-only commit's cosmo-ci run must go RED on test-windows'
lookpath check against unfixed code; the fix commit must go green.

# 2026-07-20: GOWASM=threads — threaddemo parked-M flake

## Symptom, incidence

The wasm job's threads step nondeterministically failed with
`threaddemo: FAIL: third spawn reused a parked worker M (3)` — every
functional check green, only the M-identity assertion red (fresh M 3
instead of reusing M 1/M 2). Two CI hits, both pre-B4:
https://github.com/wow-look-at-my/gosmopolitan/actions/runs/29704088679
(iteration 9) and
https://github.com/wow-look-at-my/gosmopolitan/actions/runs/29704884798
(iteration 6); measured pre-B4 CI rate ~0.37%/iteration (2 hits /
~538 iterations), ~3.6% per 10-iteration job. Locally reproduced at
post-B4 master 46b70337: 1 exact-signature failure in 1700 iterations
(~0.06-0.08%/iter) — B4 (c39b838d, PR #54) narrowed the window by
retiming wake/timer coverage but did not close it.

## Root cause: goroutine-finished != M-parked

The demo's third spawn asserted `thirdM == workerM || thirdM ==
nestedM` after synchronizing ONLY on goroutine completion:
wasmThreadsRunOnNewM resumes the locked caller the instant fn's
wrapper sends on done, while the M that ran fn still has to fall
through its scheduler tail (startlockedm -> stopm -> mput) to
sched.midle on its own host thread, concurrently. The demo comment
"both worker Ms are parked now" was an unsynchronized assumption. The
passing-run distribution proved the window routine: third spawn got M1
1582/1699, fell back to M2 117/1699 (~7% missed the still-parking M1);
the red was the tail where BOTH were momentarily absent, so startm's
mget found empty midle and newosproc took a fresh pool worker (M 3).

A fresh M there is LEGAL scheduler behavior, not a runtime bug — B4's
own comments codify transient empty-midle as expected (wakeNetPoller:
"a worker parked at add time can be claimed by startm before its
watchdog ever ticks"; checkdead: the parked main M "executes Go code
... while still linked on sched.midle"). So the fix is on the test
side; the scheduler is untouched.

## Fix: synchronize the demo on actual parked state

New read-only test hook runtime.wasmThreadsIdleWorkerMs
(os_wasmthreads.go, next to wasmThreadsCurMID): under sched.lock,
sched.nmidle minus m0-if-linked (the wasmMidleRemove membership
idiom) — i.e. worker Ms actually on sched.midle, mget-able right now.
wasmParkedWorkers was rejected (decrement-side skew: an mget-claimed M
stays counted until its futex wake lands) and raw nmidle was rejected
(the stopm-parked main M is legitimately ON midle). threaddemo polls
the hook until it reads >= 2 before the third spawn, with
runtime.Gosched — NOT time.Sleep: a sleep timer gets self-served by a
parked worker's watchdog tick, bouncing the main goroutine onto a
worker M; Gosched keeps m0 holding the only P, so no timers exist in
the window and the goroutine stays on m0. On a 10s timeout the demo
prints the distinct `threaddemo: FAIL: worker Ms never parked
(parked=N after 10s)` and exits 1 — a runtime that never parks worker
Ms is now a deterministic loud failure instead of a flake (stricter
than before, not looser).

No spawn retry needed: once >= 2 is observed (necessarily from m0 —
a worker M running the reader cannot be counted), no legal path can
claim a parked M before the spawn's lock-serialized handoffp -> startm
-> mget at GOMAXPROCS=1: m0 holds the only P continuously (wakep needs
an idle P), the watchdog's self-serve remove/re-mput is a single
sched.lock critical section, checkdead needs all Ms idle, and the
wakeNetPoller nudge only targets the main thread.

## Verification

1500/1500 CI-mirroring iterations clean at the CI config (node
22.22.2, defaults GOMAXPROCS=1/pool=4): 1100 as-is + 300 under
`taskset -c 0,1` + 100 under ~nproc busy-loop load — zero parked-M
FAILs, zero never-parked timeouts, zero other failures. Third-spawn
reuse: 1375x M1 + 125x M2, both correct. The pre-fix ~7% M2 share was
the M1-still-parking race half; post-fix M2 picks are a different,
benign mechanism — mput order is normally M2-then-M1 (nested fn ends
first), putting M1 at the LIFO head, but when M2's park loses the
host-thread-scheduling race to M1's short outer-fn tail the push order
flips and mget's head is M2 (constrained-CPU sets flip it more often:
107 of the 125 M2 picks came from the 400 taskset/cpuload iterations).

GOMAXPROCS=4 robustness probe (NOT a CI config): the M-identity
assertions are B2-era GOMAXPROCS=1 semantics — under multi-P, wakep
spawns extra pool Ms and worker/nested/third ids scatter (the in-tree
TestWasmThreadsRunOnNewM documents this and gates its identity asserts
on GOMAXPROCS(0)==1). Pre-fix demo: 9/20 identity-FAILs at
GOMAXPROCS=4; post-fix: 4/20 (not a regression, directionally better;
zero never-parked timeouts — the wait loop is multi-P-safe). CI runs
threaddemo only at the default config, where the fix is deterministic.

Full wasm CI job mirrored locally step by step (js+wasip1 std builds,
node js battery, jsfetchstream e2e, wazero wasip1 battery, wasip1sock
host suite, entire threads step incl. threaddemo 10x, the B3 demo
gates, the 4-package threads go test and the wasip1 rejection check,
wasmexport testdir on both targets): all green.

Triage note: with this fix on master, these signatures are REAL
regressions, never flakes — `threaddemo: FAIL: third spawn reused a
parked worker M`, `threaddemo: FAIL: worker Ms never parked
(parked=N after 10s)`, and the test's `re-spawn did not reuse a
parked M: M count grew A -> B`. Investigate; do not just re-run the
job (the old "re-kick a parked-M red" practice predates the fix and
is obsolete).

Same-class sibling, fixed in the same change:
TestWasmThreadsRunOnNewM's re-spawn assertion
(src/runtime/wasmthreads_test.go) had the same
goroutine-finished-vs-M-parked window, plus a mode a park-wait alone
cannot fix: extra pool Ms parked by earlier tests or earlier -count
iterations are legitimate mget picks (a parked M's watchdog tick also
re-mputs it to the LIFO head), so id-membership can be violated by a
LEGAL reuse. Demonstrated: the old assertion died within one
standalone `-count=1000` run (~1.4s of test time) as `re-spawn did
not reuse a parked M: got 1, want 2 or 3` - the failing iteration
REUSED parked M1 and still failed. Fix: under the strict
(GOMAXPROCS=1) guard, wait for wasmThreadsIdleWorkerMs() >= 2 (same
Gosched loop and 10s loud-fail as threaddemo), then assert reuse as
"M count did not grow across the re-spawn" (new WasmThreadsMCount
test export; Ms never exit on wasm, so growth == a fresh pool worker
was claimed). Post-fix: `-count=1000` standalone clean, full
`-short -count=3 runtime` clean, 4-package threads battery clean.

# 2026-07-20: GOWASM=threads — nondeterministic worker OOB trap at newMarkBits (stale atomic bounds after cross-thread memory.grow)

## Symptom, incidence

CI run 29737526856 (master f0c46edb, wasm job, first node invocation of
the threads step — the atomics smoke, GOMAXPROCS unset =1, pool 4):

    worker 4: init: RuntimeError: memory access out of bounds
        at runtime.newMarkBits (wasm-function[569]:0xa1f2c)
        at runtime.__mheap_.initSpan / allocSpan / alloc.func1
        at runtime.systemstack / alloc / __mcentral_.grow / cacheSpan
        at runtime.__mcache_.refill / nextFree

"init:" is the whole-worker-life catch (wasm_thread_run never returns;
any trap on that worker lands there — B4 made the stack visible), NOT an
instantiation failure. Local repro (4 vCPU, node 22): 12/7480 loaded runs
(~0.22% per run under 3-6 concurrent smoke instances), 0/280 idle. Same
binary green or red run to run; rate flat vs GC frequency (GOGC=10) and
allocation volume; 0/450 at pool=1 and in single-tier modes
(--liftoff-only, --no-liftoff). All 12 failures byte-identical to CI's.

## Root cause: engine-level cross-thread grow observation lag (measured)

The trap instruction (objdump) is `i64.atomic.load align=3` at
gcBitsArena.free — `atomic.Loaduintptr(&b.free)` in (*gcBitsArena)
.tryAlloc inlined into newMarkBits (mheap.go ~2905). Fresh gcBits arenas
are exactly one 64KiB wasm page from sysAlloc (mheap.go ~3020): the most
grow-adjacent atomic word in the runtime.

Mechanism, directly measured by instrumenting the worker catch
(viewPages = memory.buffer.byteLength/64Ki, the agent's possibly-stale
view; truePages = memory.grow(0), seq-cst with grows = ground truth). In
EVERY instrumented failure truePages=44 while the trapping agent's view
was 39-43 (one capture still at the INITIAL 39):

    thread A (P-holder): sbrk -> memory.grow completes; publishes the
        fresh arena pointer with correct seq-cst Go synchronization
    thread B (later P-holder): loads the pointer, first ATOMIC access
        to the fresh page -> V8 bounds-checks ATOMIC ops explicitly
        against a per-instance cached memory size that had not observed
        A's grow -> trap on a legal, committed address

Plain accesses never trap this way on 64-bit V8: they go through
trap-handler guard pages backed by truly-committed memory. Atomics are
explicit-checked in BOTH default and --wasm-enforce-bounds-checks modes
(measured rate parity). Go-side bookkeeping (bloc/blocMax under memlock,
seq-cst publication) was correct in every capture — this is
spec-permitted shared-memory behavior (cross-thread size observation may
lag), so the runtime must force the observation itself.

P2 micro-proof (hand-assembled two-agent module, scratchpad
threads-init-race/p2micro): a worker spinning INSIDE wasm on a plain
flag, released right after a main-agent wasm memory.grow, trapped on the
frontier i64.atomic.load (1/50 cold-tier) — and with a wasm-instruction
`memory.grow 0` inserted between flag-observation and the atomic, 0/50.
Wasm-side grow(0) resynchronizes the lagging instance's atomic bounds.

## Fix: assembler-emitted grow-observation guard before every atomic

One choke point covers everything: every 0xFE instruction — compiler
intrinsics and hand-written .s alike — is byte-emitted by
cmd/internal/obj/wasm's assemble(). Under GOWASM=threads it now emits,
before EVERY atomic memory access (loads/stores/RMWs AND
memory.atomic.notify/wait32/wait64 — a futex word can live in a fresh
span; atomic.fence has no memory operand and is exempt):

    i32.const $runtime.wasmGrowEpoch   ;; static addr: always in-bounds
    i32.load
    global.get $8                      ;; per-instance observed epoch
    i32.ne
    if                                 ;; rare: only after a grow
      i32.const $runtime.wasmGrowEpoch
      i32.load                         ;; pre-grow value, kept on stack
      i32.const 0
      memory.grow                      ;; resyncs THIS instance's bounds
      drop
      global.set $8
    end

runtime.wasmGrowEpoch (mem_wasmthreads.go) is bumped by sbrk under
memlock right after each successful grow, before the pointer can be
published. Correctness: the bump happens-before any publication of a
pointer into the grown region; a correctly synchronized consumer's
acquire delivers it, and the guard of its next atomic reads the epoch
after that acquire — so no atomic can address the new region with stale
bounds. Adopting the PRE-grow(0) epoch value means the observed epoch
never runs ahead of the resynced bounds. Guard and atomic are
straight-line (no calls/backedges): no goroutine migration between
resync and access. The per-instance global (index 8, i32; linker emits
it under threads only) is readable without memory access; each worker
instance gets its own. wasm_probe_atomic_add (linker-synthesized, no
runtime state) resyncs unconditionally instead. Hot-path cost: 5
instructions (i32.const + i32.load of an always-cached static word +
global.get + i32.ne + untaken if) per atomic op; ~31 bytes per site.
Non-threads builds contain no 0xFE ops and assemble byte-identically.

Deliberate residual: PLAIN shared-memory accesses rely on trap-handler
(guard-page) engines checking against truly-committed memory — true of
64-bit V8, the only supported GOWASM=threads host. An engine using
explicit bounds checks for plain accesses too could still trap a
stale-view plain access; that class is out of this guard's scope and
documented rather than covered.

## Gate + verification

testdata/wasmthreads/growatomic (CI threads step): hammer goroutines
pinned to their own worker Ms (wasmThreadsRunOnNewM), never allocating
or blocking (nothing refreshes their instances), GC off; the main thread
allocates 160 page-crossing chunks (each a fresh sbrk grow region) and
publishes first/last words via atomic pointers; hammers atomic-add
through every published pointer. Unguarded build: first touch of a
beyond-bound chunk traps within seconds. Guarded build: passes.
Verification on the fix branch: gate red on an unguarded master
toolchain / green on the fixed one; 600x 6-concurrent smoke loop with
zero traps (master baseline 8/3600); full threads regression set + all
demos green; speedup perf delta measured (see PR).

# 2026-07-20: NT — exec.LookPath never worked on Windows hosts (PATH shape + PATHEXT + env-name case)

## Symptom, evidence

A cosmo APE on a Windows host cannot resolve ANY executable by bare
name: `exec.LookPath("go")` (and therefore `exec.Command("go")`)
fails with `exec: "go": executable file not found in $PATH` even
while the same shell's `command -v go` resolves and runs go fine.
Field evidence: three go-toolchain windows-latest runs (e.g. run
29739028000 job 88342913155) — bash resolves go, the v211-built APE
in the same step does not; the same APE resolves fine on linux.

WHY IT EVER LOOKED GREEN: it never was. Pre-#60 (tool build IDs not
content-derived) the shared build cache could assemble APEs partly
from stale objects, including old embedded-PE-era lp_windows
behavior — a poison-era APE was seen logging a Windows-style
`.exe`-suffixed resolution that CURRENT source cannot produce. The
first honest clean rebuild (v211, post-#60) surfaced the real
behavior: LookPath on NT has never worked in the cosmo-native boot
era. gosmopolitan's own test-windows stayed green because neither
fizzbuzz nor runtimeprobe exercised PATH-resolved lookup on NT — a
testing gap closed in the same change (see below).

## Root cause: lp_unix compiled for cosmo, triple-fatal on NT

cosmo matches the `unix` build tag, so os/exec compiled lp_unix.go
with no cosmo override. On an NT host that is wrong three times over:

1. **PATH shape.** ntGoenvs (os_cosmo_nt.go) decodes the
   GetEnvironmentStringsW block VERBATIM — values are never
   translated — so os.Getenv("PATH") is the raw Windows string
   (`C:\hostedtoolcache\...\bin;C:\Windows\system32;...`).
   lp_unix walks filepath.SplitList (unix ListSeparator ':'), which
   shreds the value at the drive-letter colons into garbage entries.
2. **No suffix probing.** lp_unix probes bare `dir/go`; the on-disk
   file is `go.exe`. GOOS=windows probes PATHEXT suffixes; nothing
   did that here.
3. **Env-name case.** NT blocks typically spell the variable `Path`
   (pwsh/GHA runners do). GOOS=windows reads env through the
   case-insensitive OS API; cosmo's os.Getenv is the exact-match
   unix scan over the verbatim block, so even the raw value can come
   back EMPTY depending on the parent process (bash passes `PATH`,
   pwsh passes `Path`). Any fix that only fixed the split would
   still fail under pwsh-spawned processes.

## Fix: lp_cosmo.go — runtime host switch, lp_windows port on NT

New src/os/exec/lp_cosmo.go (lp_unix.go is now `unix && !cosmo`);
the host switch is `internal/runtime/syscall/cosmo`.Windows() != nil,
the same predicate package syscall dispatches on. On unix hosts
lookPath/lookExtensions are the VERBATIM lp_unix bodies (behavior
identical, control-tested). On NT they are a port of lp_windows.go
semantics — ';' split with quote handling (windows
filepath.SplitList's algorithm), PATHEXT probing (default
.com/.exe/.bat/.cmd), the implicit-cwd probe with
NoDefaultCurrentDirectoryInExePath and the full ErrDot/SameFile
machinery — with three cosmo adaptations, each documented in the
file:

1. PATH/PATHEXT/NoDefaultCurrentDirectoryInExePath are looked up
   case-insensitively over os.Environ() (last match wins, so a
   Setenv("PATH") made by the process overrides an inherited
   "Path"). os.Getenv itself stays exact-case on cosmo — scoped to
   the lookup, not a global env-semantics change.
2. Candidate-path syntax helpers (ntJoin/ntIsAbs/ntExt/ntHasExt)
   tolerate both slash flavors and drive letters. NO path
   translation happens in os/exec: the runtime's ntPathW already
   accepts raw drive-shaped paths (passthrough + slash flip), so
   Windows-form PATH entries flow through stat/open/spawn unchanged.
   ntJoin picks the separator flavor the directory entry itself
   uses, so Windows-shaped entries yield Windows-shaped results.
3. After all PATHEXT probes miss, an existing extensionless file is
   accepted as a last resort (GOOS=windows refuses). APEs are
   routinely extensionless, and CreateProcessW runs a pathed
   extensionless image fine — refusing would have REGRESSED
   exec.Command("./tool") spawns that already worked on NT.

exec.go's two `runtime.GOOS == "windows"` lookExtensions call sites
(Command's absolute-path cache, Start's resolution) now gate on a
per-lp-file `lookExtensionsEnabled()` — true on windows, ntHost() on
cosmo, false elsewhere — so `exec.Command("./tool")` resolves
`tool.exe` on NT exactly like upstream windows, while unix hosts
compile the same constant-false path as before. Bare-name
`exec.Command("go")` needs no exec.go help (it goes through LookPath),
and a drive-shaped `exec.Command("C:\...\go.exe")` also resolves via
LookPath: under cosmo's unix filepath.Base a windows path IS its own
Base, so Command's Base==name branch fires — previously nonsense
(PATH search of a windows path), now correct (ContainsAny `:\/` takes
the direct branch).

## Testing gap closed: runtimeprobe `lookpath` check

testdata/runtimeprobe/lookpath.go (wired into probeOkChecks, so all
three CI legs assert it forever): copies the probe binary into a
fresh directory as a uniquely-named dummy (".exe"-suffixed on NT),
appends that directory to PATH in the HOST's format — on NT a
genuinely Windows-shaped `C:\...` entry built from TEMP, appended
with ';' via the exact env key the block uses (usually "Path") — then
resolves the bare name via exec.LookPath AND exec.Command and RUNS
the resolved path (child-ok protocol). Where an independent raw
os.Stat scan of PATH finds a real `go` binary (all three CI legs:
apetest runs under `go test`), it additionally requires
exec.LookPath("go") to succeed — the exact field repro — and skips
that half gracefully elsewhere so no leg can false-fail.

## Verification

make.bash green (go1.24.7 bootstrap); `go build std` green for
GOOS=linux and GOOS=cosmo, both GOARCHes, plus os/exec compile checks
for windows/amd64, windows/arm64, plan9, js/wasm, wasip1/wasm;
fizzbuzz + runtimeprobe fat APEs built; full apetest suite green on
the linux host (fizzbuzz battery + 47-check runtimeprobe incl. the
new lookpath check - `ok lookpath ... (hostlookup=<real go>)` -
exercising the unix branch verbatim-behavior control); `go test
os/exec` green for GOOS=linux (upstream suite, proves the
lp_unix/exec.go seam changed nothing). Under GOOS=cosmo via the
misc/cosmo wrappers: the new lp_cosmo_test.go unit tests (the pure
nt* syntax helpers + env fold, host-independent) all pass, and the
full os/exec suite matches the PRE-change tree exactly - one
pre-existing failure either way, TestExtraFiles's `fork/exec ...
read3.exe: exec format error`, the documented misc/cosmo limitation
(a test that fork/execs a freshly built PRISTINE APE directly, no
binfmt handler on the host; A/B-verified against the unmodified
os/exec in the same build). Real-NT proof is the cosmo-ci
test-windows leg (the lookpath check against all three origin
binaries).

Still deliberately NOT changed: os.Getenv stays exact-case on cosmo
(only os/exec's lookup folds); Cmd.Env dedup (dedupEnvCase) stays
case-sensitive on cosmo-NT; no global filepath.ListSeparator change
(that would be a far bigger ABI shift — the lookup is the scoped
fix).

# 2026-07-20: NT — off-host HTTPS from an APE times out (DNS half FIXED 2026-08-30)

Field observation from consumer CI (windows-latest, fork v213, run
29741678227 of a downstream repo): the APE's HTTPS GET to
api.github.com timed out (10s client timeout) in every red run while
runner-native fetches on the same host succeeded. This is DISTINCT
from the exec.LookPath defect observed in the same runs (fixed
2026-07-20, sections above); as of 2026-07-21 no fork-side fix for
the network half has landed.

Coverage gap: runtimeprobe's TCP/UDP/AF_UNIX checks are
loopback-only, so off-host connect and name resolution from NT have
no CI probe — this failure mode is invisible to the current gauntlet.
Unverified hypothesis (first suspect, not a diagnosis): the pure-Go
resolver's unix assumptions (/etc/resolv.conf and friends) have no NT
translation in the path layer, so DNS lookups may never reach a real
nameserver. Candidate NT-backlog items: root-cause with a minimal
http.Get repro on windows-latest, an off-host network/DNS probe
(needs an endpoint reachable from CI), and resolver bring-up on NT if
the hypothesis holds.

## 2026-08-30: the hypothesis is CONFIRMED — the resolver never asks Windows

Consumer CI again (go-toolchain PR 426, windows-latest, fork v360),
this time with the error text instead of a bare timeout:

    lookup dl.pazer.build on [::1]:53: read udp [::1]:57430->[::1]:53:
    i/o timeout

`[::1]:53` is the tell. It is not a nameserver anybody configured: it
is `defaultNS` in `dnsconfig.go`, the list `dnsReadConfig` returns
when it cannot read a resolv.conf. So the resolver did reach
the network — it sent a real UDP query — and sent it to localhost,
where nothing is listening. It never had a nameserver to ask.

The path is a build-tag one, not a translation-layer one.
`dnsconfig_unix.go` is `//go:build !windows`, and GOOS=cosmo is not
windows, so an APE on an NT host compiles the file that opens
`/etc/resolv.conf`. `dnsconfig_windows.go` — which reads the
nameservers from `GetAdaptersAddresses` — is `windows`-tagged and is
not in a cosmo build at all. Nothing in the NT path layer is at
fault: the file genuinely does not exist on that host, and the unix
reader's documented fallback is exactly what ran.

The same run's other network call failed the same way and confirms
the reach is name resolution rather than one endpoint: the binary's
update check against api.github.com timed out at its 10s client
deadline, while the runner's own node-based steps resolved and
fetched over the same interface in the same job.

This narrows the 2026-07-20 observation to name resolution alone.
Off-host TCP from NT is still unproven either way — no run has yet
got past DNS to attempt a connect — so a resolver fix is necessary
and not known to be sufficient.

## The fix: ask Windows for the servers, keep the Go resolver

Two routes were available. Handing the whole lookup to ws2_32's
`GetAddrInfoW` matches what upstream GOOS=windows does, but `net` gets
there through `cgoLookupHost`, and on cosmo that comes from
`cgo_stub.go`, which sets `cgoAvailable = false` and panics if called —
so taking it means reworking `hostLookupOrder`'s choices for one host.
Filling the `dnsConfig` instead changes nothing about how a lookup runs;
only where the server list comes from.

The second is what landed, and the failure above is what makes it safe:
the resolver DID open a socket, send a UDP query and time out waiting.
Send, receive and the netpoller deadline all work on NT — only the
destination was wrong.

- `runtime/os_cosmo_nt_dns.go` loads iphlpapi lazily (the
  `ntWinsockEnsure` shape, and optional like ntdll: a host without it
  reports no servers rather than crashing) and calls `GetNetworkParams`.
  Its FIXED_INFO carries the list as NUL-terminated dotted quads, so
  nothing parses a sockaddr. The offsets are spelled as sums of their
  members and pinned by `dns_cosmo_nt_test.go` — a wrong one reads
  plausible garbage rather than failing, which on this host means
  handing net a nameserver that is not one.
- `runtime.CosmoHostDNSServers` exposes it, next to `CosmoHostOS` and
  answering only on NT.
- `net/dnsconfig_unix.go` asks, through the `hostNameservers` seam, at
  the three points where it would otherwise fall back to `defaultNS`.
  A host that publishes resolv.conf gets nil and is unaffected, and
  `defaultNS` is still returned by identity when nothing answers, which
  `isDefaultNS` compares by backing array.

`GetNetworkParams` reports IPv4 servers only. That is what stops the
queries going to localhost; a v6-only resolver setup is not covered.

Coverage: runtimeprobe grew a `dns` check, so the gap this whole entry
describes is now measured on all three runners rather than described.
It resolves a name through net's own resolver and, on failure, prints
the servers the runtime offered — the two states this entry had to be
told apart by hand. Verified red first: pointed at an unresolvable
name it reports `lookup ... on 8.8.8.8:53: no such host`, which is the
same shape as the `on [::1]:53` line that started this.

# 2026-07-21: GOWASM=threads — pool_demo printed a boot fatal and exited 0 (silent-fatal, every run since B3)

## Symptom, evidence

Green wasm-job logs on master contain, in the pool-demo slot,

    runtime: newosproc: no worker thread claimed the new M within 10s
    runtime: GOWASM=threads needs the wasm_exec_node.js worker pool (GOWASMTHREADSPOOL > 0, Node.js host)
    fatal error: newosproc: no wasm worker thread available

followed by the runtime traceback — and none of the demo's own output:
no `pooldemo: Go main is running`, no `POOLDEMO: PASS`. The step stayed
green because `node pool_demo.js` exited 0 around the fatal.

Filed as an "occasional exit-code race", but the CI record shows it was
deterministic, every run, from the B3 scheduler phase on:

- run 29701258648 (ef935b3c, B2): POOLDEMO: PASS present, no fatal
- run 29705390870 (16c08959, B3): fatal present, PASS gone — first hit
- every green master wasm job after that (checked through 29839652048,
  current master) has exactly one fatal and zero demo output

So the pool demo's payload (worker instantiation against the shared
memory, cross-instance 0xFE atomics, checksum verification) had not
executed on master CI since 2026-07-19, while the job kept certifying
it green.

## Root cause: two stacked bugs in pool_demo.js

Bug 1 — the demo never grew a runtime pool. B3 (16c08959) made
lockOSThread real under GOWASM=threads (dolockOSThread, proc.go:6295 —
package initializers, syscall/js.init in particular, must stay on the
main thread), so runtime.main's boot now parks a LOCKED main goroutine
at gcenable's channel receive (proc.go:213 -> mgc.go:216) while
bgsweep/bgscavenge sit runnable. The scheduler must hand the P to
another M: park_m -> schedule -> stoplockedm (proc.go:3831) -> handoffp
(proc.go:3585) -> startm (proc.go:3544) -> newm -> newosproc ->
wasmThreadsNewosproc (os_wasmthreads.go:83), which posts to the spawn
mailbox and futex-waits for a pool worker parked in wasm_thread_run.
pool_demo.js predates the B2 runtime pool and spawned only B1 probe
workers (stub imports, and only after boot) — the mailbox had no
servers, ever. 10s later, os_wasmthreads.go:116 threw the fatal on the
main thread. Every boot, deterministically.

Bug 2 — the fatal exited 0. The fatal path prints via wasmWrite
(synchronous fs.writeSync — that is why the log shows it), then
fatalthrow -> exit(2). runtime·exit (rt0_js_wasm.s:56) calls the
wasmExit import, sets PAUSE, RETUNWIND: wasm_exec.js records
`exited = true` and calls `go.exit(2)` — which in pool_demo.js only
ASSIGNED a variable (`exitCode = code`) that a `process.exit(exitCode
?? 1)` at the very END of the driver would consume. The driver never
got there: it was parked on `await ready`, a promise only the (dead) Go
program's __demoReady callback could resolve. The run() export returned
cleanly (PAUSE), runDone resolved unobserved, no timers or handles
remained, node's event loop drained — and an event-loop drain is a
clean EXIT 0. bash -e saw success; the step moved on to threaddemo.

## Fix (pool_demo.js), gate (cosmo-ci.yml)

pool_demo.js now pre-spawns GOWASMTHREADSPOOL runtime pool workers
(default 4) exactly like wasm_exec_node.js — goRuntime + threadRun
workerData for wasm_exec_worker_node.js, unref'd — so the post-B3 boot
handoff is served and the demo actually runs again. And the exit-code
discipline is enforced everywhere a failure can surface:

- go.exit before the final verification handoff => nonzero immediately
  (a runtime fatal's exit(2) now exits the process 2);
- a runtime pool worker's exit message, error, or thread death before
  completion => nonzero (also catches a worker-side fatal whose exit
  message was lost — the host exit hook still ends the worker thread
  with the fatal's code, surfaced by the parent's 'exit' listener);
- a wasm trap out of run/resume (runDone rejection) => exit 1;
- an event-loop drain before completion => forced exit 1 via a
  process 'exit' guard (with wasm_exec_node.js's exit-time deadlock
  probe first, so a wedged runtime still prints its stacks).

CI additionally gates the demo's OUTPUT, not just its exit code
(pipefail + tee): `POOLDEMO: PASS` must be present and no line may
match `fatal error` — so a fatal-with-green-exit cannot pass even if a
new swallow path appears.

## Verification

- Pre-fix, stock CI invocation (deterministic repro): the exact CI
  fatal, node exit 0, no demo output — the bug, on demand.
- Post-fix, stock invocation: full demo output, POOLDEMO: PASS, exit 0.
- Post-fix, original failure shape forced (runtime pool spawn disabled
  in a throwaway driver copy while the runtime still expects pool=4):
  the identical newosproc fatal now exits 2, with a
  `pool_demo: FAIL: Go program exited with code 2` trailer.
- Post-fix, GOWASMTHREADSPOOL=0: deadlock fatal, exit 2.
- Post-fix soak: 400 CPU-loaded iterations of the stock invocation
  (4 busy-loop processes on 4 cores; 300 unconstrained + 100 under
  taskset -c 0,1) — zero failures, zero fatals, POOLDEMO: PASS in
  every run.
- Full local mirror of the cosmo-ci.yml threads step (smoke, gated
  pool_demo, threaddemo x10, speedup/stwgc/liveness, growatomic,
  threads stdlib tests, wasip1 rejection): green end to end.

No src/ or lib/wasm changes — the fix is confined to the demo driver,
the workflow gate, and docs, so no toolchain rebuild and no non-threads
byte-identity proof is needed.

# 2026-07-21: stale-PR triage

PRs #35, #36, and #39 were closed 2026-07-21 as invalid/obsolete/moot;
the rationale for each is in its PR body. Do not reopen or resurrect
work from their dead head branches (`fix-ape-shell-fallback`,
`claude/fix-github-actions-logs-YXLsp`,
`claude/cross-platform-binary-VJeLb`).

# 2026-07-21: darwin sendmsg/recvmsg + SCM_RIGHTS fd passing

The last darwin socket gap from the wave-6 backlog: SYS_SENDMSG(211)/
SYS_RECVMSG(212) stayed deliberately ENOSYS on macOS hosts because
Linux and Apple disagree on every struct involved, and a half-landed
translation would corrupt buffers silently. Landed whole this time.
net.UnixConn ReadMsgUnix/WriteMsgUnix and SCM_RIGHTS fd passing over
pathname AF_UNIX stream sockets now work on macOS hosts; the
runtimeprobe sendmsg and fdpass checks lost their darwin skip legs
and are mandatory on all three hosts.

## Layout ground truth (verified against the in-tree darwin port)

msghdr (ztypes_cosmo_* vs ztypes_darwin_arm64.go; offsets identical
through Iovlen, widths diverge after):

	field       Linux/cosmo (56 B)      Apple arm64 (48 B)
	msg_name        @0  ptr                 @0  ptr
	msg_namelen     @8  u32 (+4 pad)        @8  u32 (+4 pad)
	msg_iov         @16 ptr                 @16 ptr
	msg_iovlen      @24 u64                 @24 i32 (+4 pad)
	msg_control     @32 ptr                 @32 ptr
	msg_controllen  @40 u64                 @40 u32
	msg_flags       @48 i32 (+4 pad)        @44 i32

struct iovec is {base ptr, len u64} on BOTH systems, so iovec arrays
pass through untouched - multi-iovec scatter needs no translation
(unlike NT's reversed-field WSABUF). struct cmsghdr differs
structurally: Linux {Len u64, Level i32, Type i32} = 16-byte header,
data aligned to CMSG_ALIGN = sizeof(size_t) = 8; Apple {Len u32,
Level i32, Type i32} = 12-byte header, data aligned to 4
(__DARWIN_ALIGN32). Control buffers must therefore be repacked
cmsg-by-cmsg in both directions; a whole-buffer memcpy is never
correct, and the alignment difference is exactly where a naive port
corrupts multi-record buffers.

Constants (Linux <-> Apple): SOL_SOCKET 1 <-> 0xffff; SCM_RIGHTS 1 =
1; MSG_OOB/PEEK/DONTROUTE coincide (0x1/0x2/0x4, the set
darwinCheckMsgFlags already admits); result flags differ - MSG_CTRUNC
0x8 <-> 0x20, MSG_TRUNC 0x20 <-> 0x10, MSG_EOR 0x80 <-> 0x8.
MSG_CMSG_CLOEXEC (0x40000000) has NO Apple equivalent at all.
UIO_MAXIOV is 1024 on both. Apple libc exports plain `sendmsg`/
`recvmsg` symbols (same dlsym names upstream GOOS=darwin binds), so
resolution rides the existing osArchInit dlsym batch next to
sendto/recvfrom.

## The nosplit budget forbids in-window translation (measured)

First cut did the whole translation inside the dispatch layer
(socket_cosmo_arm64.go), where sendto/recvfrom translate theirs. The
linker priced it immediately: the spine syscall.Syscall(112) ->
RawSyscall6(96) -> cosmo.Syscall6(112) -> syscall6SlowDarwin(80+80)
already burns 480 of the 792-byte nosplit budget, darwinCall ->
darwinLibcCall6 -> darwinErrno needs ~160 more on the failure edge,
and a sendmsg frame with a 112-byte sockaddr scratch plus any usable
cmsg scratch came out at 432 bytes - "nosplit stack over 792 byte
limit", 120-232 bytes over on every edge. The existing emulations fit
because their frames stay small (sendto's [112]byte sockaddr is the
prior record); an unbounded control repack never will.

So the work is split across the syscall boundary by cost:

- internal/runtime/syscall/cosmo (socket_cosmo_arm64.go,
  darwinSendmsg/darwinRecvmsg + two dispatcher cases): FIXED-SIZE
  msghdr shape adapters only. iovlen u64 -> i32 behind the
  UIO_MAXIOV bound (EMSGSIZE past it, the kernel's sendmsg/recvmsg
  spelling), controllen u64 -> u32, iovec pointer passthrough,
  result msg_flags value translation (XlatMsgFlags). msg_name and
  msg_control POINTERS pass through untouched. Frame ~100 bytes;
  the fat link's nosplit check passes.
- package syscall (new syscall_cosmo_msg.go; recvmsgRaw/sendmsgN in
  both per-arch files branch on cosmo.Darwin(), a flag only
  SetDarwinFns' XNU boot path sets): everything unbounded, as
  ordinary Go BEFORE and AFTER the _Gsyscall window - sockaddr
  translation both directions (the darwinSockaddrOut/In tables
  mirrored: {u8 len, u8 family} vs u16 family, AF_INET6 10 <-> 30,
  abstract AF_UNIX names EINVAL), and the cmsg repack via the new
  arch-independent exported helpers in socket_msg_cosmo.go
  (internal/runtime/syscall/cosmo): CmsgToApple / CmsgToLinux.
  recvmsgRaw/sendmsgN are the single funnel every caller uses
  (Recvmsg/Sendmsg(N), the Inet4/6 variants, net's ReadMsg*/
  WriteMsg* via internal/poll), so nothing else needs patching.

Resulting raw-syscall contract, documented in both files: a direct
syscall.Syscall(SYS_SENDMSG/SYS_RECVMSG) on a macOS host crosses the
boundary with Apple-shaped msg_name/msg_control BYTES (widths and
flag values still adapted). The only raw shape anything in the tree
issues - the runtimeprobe sendmsg check's two-iovec msghdrs with nil
name and nil control - is host-identical, and that check now proves
it on macos-latest.

## Cmsg repack semantics (the Linux-parity contract)

Send side (CmsgToApple, caller's oob copied into an Apple-shaped
allocation - never in place, the input must survive for retry
loops): SOL_SOCKET/SCM_RIGHTS translates with the fd payload copied
unchanged; non-SOL_SOCKET levels are silently SKIPPED (what Linux's
af_unix __scm_send does - live-kernel-verified during NT wave 3 item
2b); SCM_CREDENTIALS is EOPNOTSUPP (Linux validates credentials this
emulation cannot - NT's exact stance); unknown SOL_SOCKET types and
malformed records (cmsg_len under a header, overrunning the buffer)
are EINVAL; a final CMSG_LEN-tight record without alignment padding
is legal. A buffer that skips down to nothing sends with no control
at all, matching what Linux transmits.

Receive side (CmsgToLinux, IN PLACE in the caller's oob buffer - the
Apple shape of any payload is strictly smaller than its Linux shape,
so a Linux-provisioned buffer always has room for what Apple
delivers, and the expansion back can only overflow the caller's
CAPACITY, never the buffer): three stages so no write overruns
unread input - (1) compact translatable records to the front
(forward-safe, records only shrink), (2) plan Linux offsets against
capacity and apply fd side effects, (3) expand back to front (per
record dst >= src; payload copied high-to-low; header written after
its payload moves). Truncation mirrors the live-kernel semantics the
NT emulation pinned: SCM_RIGHTS truncates at fd granularity -
(avail-16)/4 fds, so CMSG_SPACE alignment slack still carries whole
fds ("a 24-byte buffer receives TWO fds") - every undelivered fd is
CLOSED rather than leaked, MSG_CTRUNC is raised, and the final
record may sit flush against capacity (tight-fit rule). Only
SOL_SOCKET/SCM_RIGHTS can actually arrive (darwinSockoptXlat refuses
every option that would enable timestamps and friends); anything
else is dropped.

MSG_CMSG_CLOEXEC: net's cosmo build takes the
unixsock_readmsg_cmsg_cloexec.go variant (setReadMsgCloseOnExec is a
no-op), i.e. every net.ReadMsgUnix passes the flag - so the darwin
branch strips it before the raw call and emulates it with fcntl
F_SETFD FD_CLOEXEC on each DELIVERED rights fd during the repack
walk (same post-receive window upstream GOOS=darwin's net layer
has). The dispatch layer refuses the flag EINVAL for raw callers,
exactly the NT emulation's documented stance, so a raw caller's
request is refused visibly instead of silently un-honored. SIGPIPE
needs no per-call work: every emulation-created socket already
carries SO_NOSIGPIPE (wave 2), and passed-in fds inherit their
description's options.

Known divergences, by design: SCM_RIGHTS sent on an INET socket
reaches Apple (which refuses it) instead of Linux's silent drop -
detecting the family would cost a getsockname per send for a shape
nothing issues; Apple-side record kinds that cannot arrive are
dropped rather than surfaced.

## Verification

- socket_msg_cosmo.go is deliberately arch-independent
  (//go:build cosmo, pure byte manipulation), so
  socket_msg_cosmo_test.go runs the entire repack matrix under
  GOOS=cosmo on the linux host (misc/cosmo wrappers): msghdr mirror
  offsets/sizes against ground truth, single/multi-record round
  trips, foreign-level skips, odd-payload alignment padding, error
  table (EINVAL/EOPNOTSUPP/ENOBUFS), fd-granularity truncation (the
  24-byte/two-fd kernel case), whole-record drops with fd closes,
  tight fits, malformed tails, in-place expansion byte-exactness.
  All pass; `GOOS=cosmo go test internal/runtime/syscall/cosmo` ok.
- make.bash green; fat fizzbuzz + runtimeprobe APEs build (the
  arm64 leg's nosplit check is the real gate here) and run on the
  linux host; full apetest suite green (`ok apetest`), 49 ok lines
  from the probe with `ok sendmsg` and `ok fdpass` REAL (no skip
  suffix) - the linux leg runs native SCM_RIGHTS, which validates
  the probe logic the darwin emulation must now match.
- macOS proof is the cosmo-ci macos-latest leg on this push: it
  executes the darwin dispatch (dlsym wiring, shape adapters, the
  syscall-package branches) via the same mandatory checks.

# 2026-07-21: darwin SIGPROF CPU profiling - setitimer via dlsym

The wave-9 backlog's last entry: pprof.StartCPUProfile /
-test.cpuprofile on macOS hosts produced valid, ZERO-sample
profiles. The gap was exactly one silent no-op - runtime.setitimer's
darwin asm branch ("macOS: setitimer not in Syslib ... this is a
stub") returned without arming anything, while every hz gate
(prof.hz, mp.profilehz) was set and the handler installed, so
nothing failed loudly anywhere. Everything downstream of timer-fire
already worked and was CI-proven since wave 9: setsig ->
darwinSigaction install, sigtramp's Apple->Linux signo translation
(SIGPROF is 27 on both systems), host-aware sigctxt pc/sp/lr reads
from the Apple mcontext, sighandler consuming SIGPROF before any
sigFromUser gating, and validSIGPROF (an XNU kernel-timer SIGPROF
carries Apple si_code 0, which falls into the accept-all branch;
profileTimerValid is never set on cosmo - no timer_create path).

## Fix shape: per-arch setitimer split (sigaltstack/pipe2 precedent)

- os_cosmo_arm64.go: Go dispatcher `setitimer` - isdarwin() ->
  darwinSetitimer, else setitimerLinux (the renamed asm, its Linux
  SVC byte-identical; its darwin branch is now a loud crash in the
  rtsigprocmask_darwin style, since a raw SVC would SIGSYS anyway).
  amd64 keeps its asm untouched (the raw-XNU branch is the pending
  Intel-mac bring-up path), decl relocated to os_cosmo_amd64.go.
- setitimer comes from cosmoDlsym("setitimer") like kqueue/kevent:
  the loader's Syslib (v10) exports sigaction/sigaltstack/
  pthread_kill/pthread_sigmask/raise - the signal wave already
  drives them - but has NO setitimer entry, and extending the
  Syslib stays rejected per the cosmoDlsym decision record (the
  compiled-loader cache would serve stale structs). Resolved once
  in osArchInit (cosmoDarwinSetitimerFn); zero means arming
  silently does nothing, which the probe turns into a loud FAIL -
  no new failure plumbing needed.
- Layout translation: Linux itimerval is 2x timeval{int64 sec,
  int64 usec}; Apple's is 2x {int64 sec, int32 usec, 4 pad} - both
  32 bytes, but the usec words sit at Apple offsets 8/24 (the
  tv_usec i32-vs-i64 caveat, as in wait4's rusage fixup). Mirror
  types + pure translators live in signal_cosmo_itimer.go,
  arch-independent so the unit tests run under cosmo on any host
  (the socket_msg_cosmo.go precedent); darwinSetitimer
  (signal_cosmo_xnu.go, nosplit, the darwinSigaction pattern)
  translates BOTH directions and calls through cosmoLibcCall6 (the
  libc stub is a shallow syscall wrapper - no asmcgocall needed).
  _ITIMER_PROF=2 on both systems; mode passes through. The
  int64->int32 usec narrowing is lossless by construction (set_usec
  takes int32; the profiler stores at most 1e6-1).

## The "add both together" half: pthread libcall bookkeeping

The wave-9 charter deferred upstream's m.libcallpc/sp recording in
the pthread parking wrappers until SIGPROF timers were wired ("add
both together"). Wired now: cosmoPthreadLibcCall
(os_cosmo_arm64_sema.go) is upstream sys_libc.go's libcCall ported
verbatim - record libcallg/pc/sp when libcallsp==0 (sp set LAST,
the profiler's publication order), asmcgocall, clear after;
reentrancy comment preserved - and all 7 pthread wrappers route
through it. usesLibcall() (proc.go) now lists cosmo, enabling
sigprof's libcall unwind branch: samples landing inside
pthread_cond_wait/mutex attribute to the Go caller (semasleep ->
lock2 -> ...) instead of _System. Linux hosts never set the fields,
so the branch self-disables there; the shallow syslib/dlsym
trampolines stay bookkeeping-free on purpose - those samples land
in _ExternalCode/_System, the shape the NT netpoller already
accepts.

## Probe flip + EINTR audit

testdata/runtimeprobe cpuprof: the darwin host-skip leg is deleted -
real samples (>=1 Sample record in the pprof proto) are mandatory on
all three hosts. Check names and counts are unchanged (apetest
matches "ok cpuprof" by name; 49 ok lines before and after).

100Hz SIGPROF adds interrupt frequency, not a new interrupt class:
the handler installs with SA_RESTART (translated to Apple 0x2 by
darwinSigaction), and every known non-restartable hot path was
already EINTR-hardened for SIGURG preemption - the kqueue
netpoller's registration/break/poll retries (netpoll_cosmo_xnu.go),
the sigNote pipe loops (sigqueue_note_cosmo_arm64.go), semasleep's
pthread_cond_timedwait spurious-wake loop (os_cosmo_arm64_sema.go),
and internal/poll's shared ignoringEINTR at the user level. No new
retry sites were needed.

## Verification

- Linux host: make.bash green; go vet runtime clean for cosmo
  amd64+arm64; new unit tests green under the misc/cosmo wrappers
  (GOOS=cosmo go test -run 'TestCosmoXnuItimervalABI|
  TestCosmoTimevalTranslation' runtime - Apple ABI pins: 16/32-byte
  sizes, usec offsets 8/24; translation round-trips incl. the
  hz=100 arm shape and the hz=0 disarm shape), and
  internal/runtime/syscall/cosmo stays green; the ubuntu CI leg now
  runs the runtime-package cosmo tests alongside the syscall ones.
- Fat fizzbuzz + runtimeprobe APEs build (the arm64 nosplit budget
  is the real gate); probe on the linux host: 49 ok lines with
  "ok cpuprof samples=17" - real samples through the renamed
  setitimerLinux path, proving the dispatcher end the host CI can
  measure. Full apetest suite green ("ok apetest").
- macOS proof is the cosmo-ci macos-latest leg on this push: the
  now-mandatory cpuprof check executes the dlsym resolution, the
  layout translation, the arm, and 1.2s of real SIGPROF delivery
  against all three origin binaries.

# Loop-aware inlining (2026-07-26)

## The premise, checked before writing any code

The brief was "Go won't inline a function containing a loop". The
structural form of that has not been true for several releases, and is not
true on this tree: `for`, `range` over slice/map/string/chan/int/func,
labelled `break`/`continue`, `goto` loops, and loops containing `switch` or
`select` all report `can inline`. What remains structurally rejected is
`OGO`, `ODEFER`, `OTAILCALL`, and `call to recover`.

What is still true is the cost model, in a way that makes the complaint
accurate in practice. `hairyVisitor` charges one unit per IR node
regardless of how often that node runs, and `CanInline` compares the total
against a flat 80. A loop body is where a function's nodes are, so a
function that does real work in a loop blows the budget while a
straight-line function half its size fits. Over std: 10,426 functions
rejected as "function too complex", median cost 203, p25 129.

## The three mechanisms

`src/cmd/compile/internal/inline/loop.go`. Loop nesting is used as the
static stand-in for a profile, as GCC (growth divided by a
frequency-weighted benefit), LLVM (block-frequency-driven thresholds) and
HotSpot C2 (frequency-scaled limits) all do:

1. Cost, callee side: a node inside a loop is charged
   `1/inlineLoopCostDivisor` of its usual cost, capped by
   `inlineLoopCostCredit`. Tracked by a new `OFOR`/`ORANGE` case in
   `hairyVisitor.doNode` that counts what the loop body spends, and
   subtracted in `hairyVisitor.cost()`. Folding the credit into `Inl.Cost`
   means every downstream consumer - call-site limit, big-caller limit,
   PGO, inlheur - sees the discounted number with no export-format change.
2. Budget, call-site side: `inlineCostOK` takes a `loopDepth` and grows
   `maxCost` by `inlineLoopSiteFactor` per level up to
   `inlineLoopMaxDepth`, ceiling `inlineLoopHotBudget`. The depth comes
   from `interleaved`'s existing `mark` walk; a newly inlined body is
   re-marked at the depth of the call site it landed in, so calls pulled
   into a loop are treated as being in it.
3. Availability, callee side: `markLoopCallees` runs before the bottom-up
   `CanInline` pass and records what this package calls from inside a loop;
   those callees are analyzed with the larger budget, exactly as PGO does
   for profile-hot functions. Without this, part 2 has nothing to inline -
   a callee over budget never gets an inline body at all. It is
   intra-package by construction, since inlinability is decided when the
   callee's own package is compiled.

Plus a per-caller growth allowance that part 2 draws from: inlining into a
loop makes more of the caller loop-nested, which admits more inlining, and
that feedback needs a stop.

## Two runtime annotations, both found by CI, neither a workaround

Both are upstream inconsistencies that only surface once more code is
inlined. An inlining change is a call-graph change, and the runtime's
`nowritebarrierrec` checker sees call graphs.

- `runtime/unsafe.go`: `panicunsafestringlen` and
  `panicunsafestringnilptr` lacked the `//go:yeswritebarrierrec` that
  their `unsafe.Slice` equivalents twenty lines below already carry.
  `abi.Name.Name` reads a varint in a loop, so it became inlinable, so
  `rtype.string` acquired a direct call to `panicunsafestringlen`, so
  every `nowritebarrierrec` root reaching a fatal-error print (`cgoBindM`
  -> `fatal` -> ... -> `rtype.string`) was rejected for a panic that
  cannot occur.
- `runtime/panic.go`: the `goPanicSlice3*` family and
  `goPanicSliceConvert` lacked it while the 2-index `goPanicSlice*`
  helpers have it. Visible only on GOARCH=wasm, where bounds checks call
  these Go functions rather than assembly `panicBounds*`: the chain was
  `(*mLockProfile).store` -> `storeSlow` -> `saveBlockEventStack` ->
  `stkbucket`'s 3-index slice -> `goPanicSlice3AlenU` -> `gopanic`.

Operational lesson for this tree: build `runtime` for js/wasm as well as
the host before believing an inlining change is clean. wasm reaches panic
paths no other target does, because its bounds-check helpers are Go.

## Two more CI rounds

- `loopSiteMaxCost` applied its ceiling as a plain `min`, so a ceiling
  below the call site's own limit would have lowered it. Unreachable with
  the shipped constants; fixed anyway rather than left depending on them.
- `inlheur`'s `returns2.go` golden scores moved by 7, because its
  `newBar`/`meaning` helpers contain loops and a callsite score is the
  callee's cost plus adjustments. Only numbers moved - no property or
  adjustment classification changed, and the other five props testcases
  came back byte-identical. Regenerated with `-update-expected`.

## Tuning: what the measurements changed (2026-07-26)

All three mechanisms were implemented, then measured, and the measurement
moved two of them and killed the third's default.

The methodology mattered more than the compiler did. Configurations
benchmarked one after another are confounded by machine drift: running the
BASELINE against ITSELF, second, produced +1.3% median across 67
benchmarks, 37 of them "slower". Three rounds of numbers - a -1.5% "win",
then a +6.1% "loss" - were artifacts of run order and were thrown away.
Only round-robin interleaving, where every configuration is measured in
every round, gives differences that survive. The same trap caught the
macro benchmark first, which is why it was interleaved from the start and
why it is the one that always said "no difference".

Interleaved results, nine whole-task workloads (1.8MB JSON encode/decode,
deflate/inflate 100KB, regexp medium+hard, sort ints/strings, go/parser,
text/template), 8 rounds:

| config | median | faster/slower >1% | text |
|---|---|---|---|
| call site + availability | -1.1% | 6 / 1 | +5.00% |
| callee cost discount | +1.1% | 3 / 5 | +1.76% |
| all three | -1.6% | 5 / 2 | +7.32% |

And on a benchmark built to be the complaint itself - a hot loop calling
helpers of cost 90/84/125 that the flat budget rejects and this change
accepts at 49/43/72 - 10 rounds interleaved: call site -2.3% median, best
-22.5% (trim), -4.8% on a mixed pipeline; discount -2.3% median.

Reading: loop nesting is worth acting on at the CALL SITE, not at the
callee. The discount makes a loop function cheap at every call site
including the cold ones, so its growth lands everywhere while its benefit
only materializes where the call is hot - it helps the targeted shape as
much as the call-site path does, and costs 1.1% everywhere else.
Consequence: `inlineLoopCostDivisor` defaults to 0 (off), reachable with
`-d=loopinlinediv=2` for a workload built out of small hot kernels.

Compiling std is unchanged: 17.5-18.1s whichever compiler does it, with
the between-config spread smaller than the within-config spread.

Effect on std with the shipped default: 1,489 more functions inlinable
(15,888 -> 17,377), 4,967 more call sites inlined (70,146 -> 75,113).
The inlheur props goldens are untouched, because with the discount off
the measured costs are exactly upstream's.


# 2026-07-26: go1.26.5 uprev - a clean merge, and what the divergence measurement says

Merged upstream tag go1.26.5 (12 upstream commits on top of the
go1.26.4 base: the os.Root trailing-slash fix, the ECH outer-hello PSK
omission, the moveSliceNoCap element-size rounding, the Linux kernel
release vendor-suffix parse, and friends). One conflict, VERSION,
resolved to `go1.26.5cosmo` - the same single conflict the go1.26.4
uprev had.

Unlike go1.26.4, nothing broke: `GOOS=cosmo go build std` was clean on
amd64 and arm64 on the first try, with zero cosmo fixes needed. The
delta touched no build tags and added no new platform-partition files,
which is exactly the property that made it free.

**Verified**: make.bash (go version go1.26.5cosmo linux/amd64); std for
cosmo/amd64 + cosmo/arm64; fat fizzbuzz + runtimeprobe (45 ok checks,
including preempt/cpuprof/ctrlbreak/fdpass); apetest 129 PASS 0 FAIL
against the local fat APEs; cmd/link ld APE/PE/Macho tests; cmd/go
strip + GOCOSMODEBUG + tool-ID tests; GOOS=cosmo
internal/runtime/syscall/cosmo and the runtime cosmo tests via the
misc/cosmo wrappers; host os, os/signal, crypto/tls, go/build,
cmd/internal/moddeps.


## Two guardrails that had gone red without anyone noticing

`go test go/build` failed on this tree, and had been failing since well
before this merge:

1. `TestVendorPackages` rejected `github.com/klauspost/compress`,
   vendored into src/cmd/vendor for the ELFCOMPRESS_ZSTD DWARF
   compression (round 2, 2026-07-20). The test's own instructions are
   to allowlist a deliberately vendored package; that was never done.
2. `TestDependencies` had no policy entry for
   `internal/runtime/syscall/cosmo`, so all four of its importers
   (itself, runtime, syscall, os/exec) reported as unexpected
   dependencies.

Neither is cosmetic. `TestDependencies` is the only mechanical check
that the fork's new packages sit where the tree's layering says they
may sit; while it is red for a bookkeeping reason it cannot report a
real layering break, and an uprev is precisely when one would appear.
CI never caught either because the only go/build test it ran was
`-run TestReadGoInfo`, inside the shebang step. Both fixed, and
go/build + cmd/internal/moddeps now run on the ubuntu build leg.


## The uprev tripwire: CI compiled 84 of 358 std packages under cosmo

The two go1.26.4 build breaks (`unix.Fchmodat undefined` after upstream
split at.go into fchmodat_linux.go + fchmodat_other.go;
`f.lstatatNolog undefined` after statat_unix.go got a new tag) share one
shape: **upstream re-partitions a platform file and cosmo falls off the
edge of the new build tags.** No conflict is produced - the merge is
clean and the breakage is a compile error somewhere in std.

CI could not see that class at all. The cosmo legs build only
fizzbuzz.com and runtimeprobe.com, which between them reach 84 of the
358 std packages that exist under GOOS=cosmo - 23%. The uncovered 77%
includes crypto/x509, archive/tar, net/http and most of the packages
that actually carry the fragile tags: `crypto/x509/root_unix.go` and
`archive/tar/stat_actime1.go` are both files whose entire fork delta is
adding `cosmo` to a `//go:build` line, and neither was being compiled.

`GOOS=cosmo go build std` for both arches now runs on the ubuntu build
leg. Measured 38s cold for the pair - the cheapest coverage in the
workflow.


## Where the fork's merge liability actually lives

Measured against go1.26.5, over src/ test/ lib/ api/ misc/:

| | files | merge cost |
|---|---|---|
| new files added by the fork | 255 | none - they cannot conflict |
| upstream files, build-tag edit only | 46 | trivial to merge, but the silent-break surface above |
| upstream files, substantive edits | 173 | the real liability |

The 255-to-0 shape of the first row is the fork doing the right thing:
cosmo's runtime, syscall layer and NT/darwin emulation live in new
`*_cosmo*.go` files that upstream will never touch.

The surprise is the third row's composition. Ranked by churn, the
fork's most-diverged upstream files are:

| file | added lines | wasm/threads | cosmo |
|---|---|---|---|
| src/runtime/proc.go | 818 | 256 | 8 |
| src/cmd/internal/obj/wasm/wasmobj.go | ~470 | all | 0 |
| lib/wasm/wasm_exec.js | ~438 | all | 0 |
| src/cmd/link/internal/ld/pcln.go | 340 | 0 | 4 |
| src/runtime/symtab.go | 234 | 0 | 0 |
| src/cmd/compile/internal/inline/inl.go | 110 | 0 | 0 |

`runtime/proc.go` is the single most-diverged file in the tree and it is
**not a cosmo file** - it is the wasm threads scheduler work (B2-B4).
The cosmo port, the thing this fork exists for, is close to free to
merge. What costs is the other three product lines the fork carries -
wasm rounds 1-8 and threads B0-B4, the compact pclntab, and loop-aware
inlining - all of which are implemented as edits to hot upstream files
rather than as new ones.

That reframes "make uprevving easier": the lever is not more cosmo
modularity, it is giving the non-cosmo features the same
new-file discipline cosmo already has. See the PR discussion for the
specific candidates (hook-and-implementation splits for proc.go's
threads code, and a pclntab writer/reader pair that is already
lockstep-coupled by CLAUDE.md's own warning).


# 2026-07-26: macOS CI wedge datapoint - checkFdpass hangs in forkExec

Logging another nondeterministic macOS wedge, same spirit as the
2026-07-05 entry. Surfaced on PR #74 (the go1.26.5 uprev) and,
independently and slightly earlier, on PR #72, which does not contain
that merge.

**Symptom.** The runtimeprobe `fdpass` check wedges on macos-latest and
the 90s watchdog fires:

    FAIL watchdog: probe did not finish within 90s
    panic: watchdog: probe wedged; all-goroutine traceback follows
    goroutine 1 [syscall]:
    syscall.Syscall(0x3f, 0xf, ..., 0x8)        # 0x3f = read
    syscall.readlen(...)                         zsyscall_cosmo_arm64.go:524
    syscall.forkExec(...)                        exec_unix.go:220
    os/exec.(*Cmd).Start(...)
    main.checkFdpass()                           fdpass.go:132

exec_unix.go:220 is the parent's blocking read of the child status pipe.
The parent forked and is waiting for the child to either write an errno
or close the write end at exec. It never does.

**Nondeterministic, and not the uprev.** Each macOS test job runs the
probe once per origin binary (ubuntu, macos, windows), and which origins
wedge varies run to run:

| run | branch | ubuntu | macos | windows |
|---|---|---|---|---|
| 30217534778 | PR #72 (no 1.26.5 merge) | FAIL | pass | pass |
| 30217973653 | PR #74 | FAIL | pass | FAIL |
| 30218189383 | PR #74 | pass | FAIL | pass |
| 30218365587 | PR #74 | pass | pass | pass |

Same binaries, different outcomes - so it is timing, not a code break.
PR #72 reproduced it at 19:53, before PR #74's first macOS leg ran at
20:07. Attribution was also checked from the other direction: of the 20
non-test .go files go1.26.5 changed, exactly six are compiled into a
cosmo/arm64 runtimeprobe (os/file_posix.go, os/root.go,
os/root_openat.go, os/root_unix.go, os/signal/signal.go,
runtime/slice.go) and none is on the fork/exec path. The merge's actual
darwin fork/exec changes - the `//go:norace` annotations on
exec_libc2.go's forkAndExecInChild and syscall_darwin.go's rawSyscall -
are in `//go:build darwin` files that GOOS=cosmo never compiles.

**Ruled out.** Concurrent-fork leakage of the status pipe's write end,
the classic cause of this exact hang: exec_unix.go's forkExec holds
ForkLock across both forkExecPipe and forkAndExecInChild, so Go's own
forks are serialized against pipe creation.

**Open hypothesis, unproven.** cosmo selects forkpipe2.go (the "atomic
pipe2" variant) because it advertises SYS_PIPE2, but on a darwin host
that syscall is emulated: Apple's libc has no pipe2, so
runtime.pipe2 (os_cosmo_arm64.go) calls the Syslib's flagless `pipe`
and then applies FD_CLOEXEC with fcntl. Any fd created that way is
briefly non-CLOEXEC. That is exactly the window forkpipe.go's
non-atomic variant exists to document, and the tree's ForkLock
discipline only covers pipes created *through* forkExec - not one
created concurrently elsewhere in the runtime. A write end leaked into
any live child would hold the parent's read open forever, which is the
observed symptom. Unverified: reproducing needs a macOS host, and CI is
the only one available.

**Not fixed here.** Rate is roughly 4 wedges in 15 origin-executions
observed post-2026-07-26; a rerun clears it (30218365587 went green on
all three origins). Chasing it properly means auditing every darwin fd
creation for the CLOEXEC window and probably giving cosmo's darwin
dispatch a real atomic pipe2 - a runtime change that wants its own
change and its own in-CI forensics, the way the wave-9 XNU read(2)
wedge did.


# 2026-07-26: fat APE builds run both architectures in parallel

The fat build spent its whole life doing one architecture at a time, and
the reason turned out to be an implementation artifact rather than a
constraint. `cosmoFatten` was a POST-build step: `go build` finished the
primary architecture, and only then did it re-execute the entire go
command as a child process with GOARCH flipped
(`cosmofat.go`, `cmd.Env = append(os.Environ(), "GOARCH="+otherArch, ...)`),
and merge. Re-running the command line was a good way to inherit every
build flag exactly; running it afterwards was never necessary. The two
architectures share nothing that forces an ordering - different GOARCH,
different build-cache keys, different output paths - and cmd/go's build
cache is safe for concurrent processes.

**Measured** (runtimeprobe, cold cache, 4 cores, the two modes
interleaved so machine drift cancels):

| | wall | user | note |
|---|---|---|---|
| sequential (`GOCOSMOFATSEQ=1`) | 15.7s, 15.0s | 36.8s | |
| parallel (default) | 12.3s, 12.6s | 37.5s | **~19% faster** |
| `go build std`, both arches | 36.8s -> 35.8s | 112s -> 118s | 3%, noise |

User time is unchanged, so this is pure overlap, not extra work. The win
comes from the serial tail: a single main package leaves cores idle while
it links (and cosmo links twice per architecture, once per payload), and
that tail overlaps the other architecture's compile phase. Whole-graph
builds like `go build std` already saturate the CPU - 3.06x on 4 cores
before the change - so there is nothing left to reclaim, which is why the
`go build std` CI step stays sequential.

**Output is byte-identical** to a sequential build, APE and both debug
sidecars, verified with the same toolchain and commit on both sides. (Do
not compare across a `make.bash`: tool build IDs are content-derived, so
rebuilding the toolchain legitimately changes the output, and intervening
commits move the VCS stamp too. The controlled comparison is same
toolchain, same commit, parallel vs sequential.)

**Three things the restructure had to get right**, none of which the
old post-build shape had to worry about:

1. **Orphans and leaked scratch dirs.** The primary build can now fail -
   and `base.Fatalf` exits - while the sibling is still running. The
   sibling registers a `base.AtExit` hook that kills the child and
   removes its temp directory. Verified: a failing build leaves zero
   `/tmp/gocosmofat*` directories and zero stray go processes.
2. **Interleaved diagnostics.** Two concurrent builds writing to one
   terminal shred each other's error messages. The child's stdout and
   stderr go to a buffer that is replayed verbatim after the primary
   build's own output. Better still, the build paths now call
   `base.ExitIfErrors()` before fattening, so a compile error that fails
   both architectures is printed ONCE - the old sequential code ran the
   sibling after a failed primary build and printed every error twice.
3. **Work that should never start.** `-o /dev/null` cannot be fattened
   (fattening re-reads and re-creates the target), and a library-only
   `go install` has no main package to merge. Both are now decided
   BEFORE the sibling starts, via `cosmoFatSkipOutput` and the
   main-package count, rather than by a post-build filter that would
   have arrived too late to save the work.

`GOCOSMOFATSEQ=1` restores the sequential behavior. It exists for
memory: the two link phases can otherwise overlap, and linking is the
memory-hungry part of a cosmo build, so a constrained machine has a knob
that is not `GOCOSMOFAT=0` (which gives up fat binaries entirely).

**Verified**: cmd/go/internal/work (incl. new TestCosmoFatParallel and
TestCosmoFatSkipOutput), cmd/link/internal/ld APE/PE/Macho, go/build +
cmd/internal/moddeps; fat `go build` and `go install` both produce
running 45/45 probes with correct sidecars; thin `GOCOSMOFAT=0` builds
unaffected; apetest 129 PASS 0 FAIL against parallel-built binaries.


# 2026-07-26: the macOS fork/exec wedge - ROOT CAUSED and fixed (arm64-apple variadic ABI)

Supersedes the datapoint entry earlier today, which recorded the wedge
as nondeterministic and left two candidate explanations open. Both were
wrong. The cause is an ABI bug, it was never a flake, and re-running CI
until it passed was hiding a defect that affected far more than exec.

## What it looked like

The runtimeprobe `fdpass` check wedged on roughly a quarter to a half of
macOS runs. The parent parked forever in forkExec's read of the child
status pipe (exec_unix.go:220 readlen), the 90s watchdog fired, and the
all-goroutine traceback showed only goroutine 1 in that read plus the
watchdog.

## The two dead ends

**"A concurrent fork leaked the write end."** Killed by the traceback:
only two goroutines exist, so nothing else in the parent was forking.

**"The child is stuck between fork and exec."** Killed by instrumenting
the child: it stamps a descriptor census as its first act after exec,
and the census was always there. The child exec'd fine.

Also killed, earlier and more cheaply than either: `execstress`, a
60-round fork+exec loop, passed on macOS. Plain exec was never broken.

## What the census showed

Adding a descriptor census to both sides (fcntl+fstat only, so it works
when process creation is the suspect) produced the answer immediately:

    parent fds: 3:pipeE ... 11:pipe- 12:pipe- 13:pipe- 14:pipe- 15:pipe-
    child  fds: 3:pipe- ... 11:pipe- 12:pipe- 13:pipe- 14:pipe- 15:pipe- 16:pipe-

Descriptor 10, which carried FD_CLOEXEC, WAS closed by the exec - so
exec honors the flag. The pipes simply never had it, and the child came
out of exec holding 16: the status pipe's write end. The parent was
waiting for an EOF that could not arrive.

A direct assertion over every creation path (`cloexec`) confirmed the
scope:

    FAIL cloexec: pipe2(O_CLOEXEC) read end: FD_CLOEXEC NOT set;
      pipe2(O_CLOEXEC) write end: FD_CLOEXEC NOT set; os.Pipe read end:
      FD_CLOEXEC NOT set; os.Pipe write end: FD_CLOEXEC NOT set;
      socket(SOCK_CLOEXEC): FD_CLOEXEC NOT set

`open(O_CLOEXEC)` passed in the same run. That asymmetry is the tell:
every failing path sets the flag with a separate `fcntl(F_SETFD,
FD_CLOEXEC)`, while open takes O_CLOEXEC as its ordinary fixed flags
argument.

## Root cause

**arm64-apple passes variadic arguments on the STACK, never in a
register, even when argument registers are free** - a deliberate
divergence from AAPCS64. cosmo's libc trampolines (`cosmoLibcCall6` and
the assembly fast path's `darwin_call`) put every argument in registers.

`fcntl(int, int, ...)` is variadic. So every
`fcntl(fd, F_SETFD, FD_CLOEXEC)` handed FD_CLOEXEC to Apple's libc in
R2, which read that argument from uninitialized stack memory instead.
The value wanted is a flag word, so the call SUCCEEDED while setting
close-on-exec from whatever was on the stack - set when the garbage
happened to be odd, clear otherwise. That is the entire "flake": not
timing, not scheduling, just whichever value was lying at that stack
slot.

Why only `fdpass`: a leaked status-pipe write end is invisible when the
child exits promptly, because the exit closes it and the parent's read
completes. `fdpass`'s child waits for the parent to accept its
connection, so the leak becomes a true deadlock - parent waiting on the
child's exec, child waiting on the parent. `execstress` originally
missed it for exactly this reason and was rebuilt around the real
shape (fd pressure + a child that waits on a go-ahead the parent can
only give after Start returns).

Two long-standing oddities were the same bug all along: the
`F_DUPFD_CLOEXEC` EINVAL on the macOS runner, recorded in the dispatch
as an unexplained quirk with plain `dup(2)` added as a fallback, and any
socket left blocking because `F_SETFL`/O_NONBLOCK silently did nothing.

## The fix

`runtime.cosmoLibcCallVariadic1` (sys_cosmo_arm64.s) and
`darwin_call_v3` (the fast path) place the variadic argument on the
stack. Every fcntl call site routes through them. `openat` is fixed the
same way - its mode is variadic, so every O_CREAT open had been taking
its permission bits from stack garbage.

## Gates

- `cloexec` asserts FD_CLOEXEC really is set by pipe2(O_CLOEXEC),
  os.Pipe, open(O_CLOEXEC), socket(SOCK_CLOEXEC) and
  fcntl(F_DUPFD_CLOEXEC), reporting each mechanism separately. ENOSYS is
  accepted for F_DUPFD_CLOEXEC alone (NT does not implement dup for
  files or pipes) and is surfaced in the ok line; every other error
  fails, so the EINVAL this bug produced stays a failure.
- `execstress` reproduces the deadlock shape deterministically.
- Both censuses print on a wedge, so a future regression arrives with
  evidence instead of a bare traceback.

**Verified**: macos-latest went from wedging on 3 of 6 origin-executions
to green on all three origins, with `ok cloexec`, `ok fdpass`,
`ok execstress` and `sockpairpoll dupcloexec=ok` (previously
`dupcloexec=invalid argument`). Linux and Windows stay green.

## The lesson worth keeping

A test that fails a third of the time and passes on re-run is not a
flake; it is a defect with a probability attached. This one was a silent
ABI mismatch corrupting close-on-exec, non-blocking flags and file
permissions across the whole darwin port, and every re-run to green
bought another day of it. The thing that cracked it was making the
failing run PRINT its evidence - the descriptor census - rather than
reasoning harder about a traceback.

---

# Platform-subset APEs (GOCOSMOPLATFORMS, 2026-08-14)

A cosmo APE claimed every host the fork can boot on, whether or not the
build wanted them. `GOCOSMOPLATFORMS` names the hosts a build must cover;
cmd/link's `-apeplatforms` turns that selection into the boot mechanisms
the header carries.

## Platform -> what the binary needs

| platform | payload | header pieces |
|---|---|---|
| linux/amd64 | amd64 | printf'd amd64 boot ELF header (self-assimilation) |
| linux/arm64 | arm64 | printf'd arm64 boot ELF header (self-assimilation) |
| darwin/amd64 | amd64 | Mach-O header at 0x1000 + the `dd ... conv=notrunc` stanza |
| darwin/arm64 | arm64 | gzipped APE loader source at 0x8000 + the arm64 printf |
| windows/amd64 | amd64 | the real NT PE header at 0x80 + the MSYS/Cygwin `cmd //c` line |

Two traps in that table:

- **darwin/arm64 needs the arm64 printf even though it never runs it.**
  The macOS loader finds the aarch64 image by decoding every `printf '`
  in the file's first 8192 bytes (ape-m1.c). Selecting darwin/arm64
  without linux/arm64 therefore emits the printf as unreachable shell,
  after the `exit 1`, purely so the loader can find it.
- **windows/amd64 boots the cosmo AMD64 payload**, not a payload of its
  own. Dropping it saves no payload, only the real PE header - the stub
  takes its place.

## Where the size actually goes

The APE header is a fixed 64K region, so dropping the Mach-O header, the
loader source or the real PE header saves **zero bytes**. Only dropping a
payload architecture changes the size. runtimeprobe, every row built by
one command with `-buildvcs=false -ldflags=-buildid=`:

| selection | payloads | bytes | vs fat |
|---|---|---|---|
| unrestricted (all five) | amd64+arm64 | 5,796,992 | - |
| linux/amd64,darwin/arm64,windows/amd64 | amd64+arm64 | 5,796,992 | 0% |
| linux/amd64,linux/arm64,windows/amd64 | amd64+arm64 | 5,796,992 | 0% |
| linux/amd64,windows/amd64 | amd64 | 3,072,000 | -47.0% |
| linux/amd64 | amd64 | 3,072,000 | -47.0% |
| darwin/arm64 (GOARCH=arm64) | arm64 | 2,782,336 | -52.0% |

Sidecars follow the payloads: an amd64-only build writes only the
4,362,818-byte `.dbg`, so the 3,912,537-byte `.aarch64.elf` goes too.

**A platform subset is not a size win unless it collapses to one
architecture.** The set this feature was asked for - linux x64, macOS
arm64, Windows x64 - weighs exactly what the unrestricted APE weighs,
because darwin/arm64 boots the arm64 payload and the other two boot the
amd64 one, so both are still in. What it buys is an accurate claim and a
host that is refused by name instead of failing mysteriously. Do not
report it as slimming.

Payload-side slimming - dropping the NT emulation or the darwin syslib
dispatch from a payload no selected platform boots - is a compile-time
change to the runtime's build tags and is NOT part of this; every payload
still carries support for every host.

## cmd/go

`cosmoSiblingArch` decides whether the sibling-architecture build runs at
all: a selection whose platforms all boot one payload skips it entirely,
which is the whole saving. `cosmoAssembleEnabled` then keeps the linker's
assembly step (`-apefat` with ONE input) so a restricted build is still
stripped and still gets its sidecars - without it, a one-architecture
selection would silently produce an unstripped binary with no sidecar,
which is not what "same build, fewer platforms" should mean.

The selection is passed to the assembly step only, never to the thin
links. Those keep producing a full-platform header, which is why the
amd64 thin APE still carries the real NT header the fat merge
transplants.

## Refusals

- an unknown or empty platform token, in either the variable or the flag;
- a selected platform whose payload was not built;
- a payload no selected platform boots;
- `GOCOSMOFAT=0` with a selection that needs both payloads;
- a GOARCH the selection does not boot (`rerun with GOARCH=...`);
- windows/amd64 selected while the amd64 input carries only the
  do-nothing stub PE header (checked by section count: the real header
  declares 3, the stub 1).

The last one only fires when the flag is explicit. With no flag the
linker keeps its old behavior of supporting whatever the payloads allow,
which is what makes an unrestricted build byte-identical to before.

## The destructive case worth knowing

Deselecting darwin for an architecture whose Linux platform stays
selected used to leave the macOS branch running the Linux path: the
`printf` writes a boot ELF header over the APE header, and with no Mach-O
header to `dd` back over it the file is left neither an APE nor a
Mach-O - a binary that destroys itself on first run. The bootstrap now
refuses macOS by name BEFORE the printf in that configuration
(`[ -d /Applications ] && { echo ...; exit 1; }`).

## Gates

- `cmd/internal/cosmoape` - the token table, canonical ordering, and
  every rejected spelling.
- `TestAPEPlatformsHeaderPieces` (cmd/link/internal/ld) - per selection,
  which payloads, boot headers, Mach-O header, loader source, cmd.exe
  delegation and unsupported-host message are present, and which must
  not be.
- `TestAPEPlatformsDefaultUnchanged` - an unset flag assembles
  byte-identically to naming every platform.
- `TestAPEPlatformsRejects` - the refusals above, in process, via the
  linker's `-h` panic mode.
- `TestCosmoSiblingAndAssemble` (cmd/go/internal/work) - the sibling and
  assembly decisions per selection.
- `env_cosmo.txt` (cmd/go script test) - `go env` reports the effective
  GOCOSMOPLATFORMS/FAT/STRIP/DEBUG. A consumer probes GOCOSMOPLATFORMS to
  tell a toolchain that honors the selection from one that ignores it, so
  this readout is load-bearing outside this repo.
- `TestSlim*` (testdata/ape/apetest) - the same absence checks against a
  real binary, plus executing it. cosmo-ci builds two subsets on the
  ubuntu leg and runs them on all three test legs, so a subset that
  stopped booting on macOS or Windows fails there rather than in a
  consumer's hands.

**Verified 2026-08-14**: with GOCOSMOPLATFORMS unset, fizzbuzz.com,
runtimeprobe.com and all four sidecars are byte-identical (sha256) to the
same builds from the pre-change tree, comparing with `-ldflags=-buildid=`
and `-buildvcs=false` so the tool-ID and VCS stamps do not mask the
comparison. The linux/amd64-only runtimeprobe passes the full apetest
runtimeprobe suite on this host with no arm64 payload in the file.

# 2026-08-20: uprev to go1.27.0

Merged upstream go1.27.0 (previous base: go1.26.5). The merge itself is
documented in docs/UPREV-GO1.27.md - why a minor bump conflicts where a
patch bump does not, how to separate release-branch noise from real
disagreement, the resolutions worth knowing about, and the internal APIs
that moved.

Five things broke that no conflict marker pointed at:

- `decoderune` now takes and returns a `uint` index (os_cosmo_nt_path.go).
- The ssa generator's `regMask` became a struct, so the fork's Wasm ops
  moved to `union`/`regMask{}` and opGen.go and rewriteWasm.go were
  regenerated rather than hand-merged.
- `internal/runtime/atomic` moved its linkname exports into linkname.go
  under `//go:linknamestd`, and a second `//go:linkname` for the same name
  is a compile error now. The GOWASM=threads atomic file carried a
  duplicate block.
- `gp.m.locks` is scaled by `mutexMLocksDelta` for mutexes;
  lock_jsthreads.go was still changing it by one.
- `cmd/link` now rejects a cross-package assembly reference to a symbol
  whose DEFINING object carries no linkname push. This hit five cosmo
  symbols. Two (`__hostos`, `__syslib`) had a Go var holding the push and a
  shadowing `DATA`/`GLOBL` pair in rt0 assembly - the assembly definition is
  the one the linker resolves, so the push never reached it; deleting the
  assembly definitions fixes it. Three are assembly-only helpers and now
  carry Go declarations that exist purely to carry the push.

Verified on linux/amd64: make.bash; cosmo std for amd64 and arm64; std for
js/wasm and wasip1/wasm, each also under GOWASM=threads; the go/build and
cmd/internal/moddeps guardrails; fat APEs of fizzbuzz and runtimeprobe with
both sidecars, executed here; and the full apetest suite against both.

# 2026-08-30: the Windows trust store

go-toolchain's `smoke-windows` job downloaded a release over HTTPS and got
`x509: certificate signed by unknown authority`, on every certificate,
`api.github.com`'s included. A certificate every other host accepts is not
the suspect: the root pool was empty.

Every path in `crypto/x509`'s `root_cosmo.go` is a unix path
(`/etc/ssl/certs/...`, `/etc/pki/...`). Windows ships none of them, so the
scan found nothing, `loadSystemRoots` returned an empty pool with no error,
and every verification failed. This is the resolv.conf gap one layer up,
with the same cause: a cosmo binary compiles the unix side of an upstream
platform split, and NT keeps the answer somewhere else.

Upstream windows Go does not load roots at all - it hands the chain to
`CertGetCertificateChain` through `systemVerify`, which is a no-op stub
under the `cosmo` tag, and reaching that engine means a `syscall` surface
GOOS=cosmo does not have.

The roots now come from crypt32's ROOT store, read by the runtime
(`os_cosmo_nt_certs.go`) the way the nameservers come from iphlpapi beside
it: `CertOpenSystemStoreA`, then `CertEnumCertificatesInStore` to walk it,
copying each context's DER into Go memory. `runtime.CosmoHostRootCerts`
answers only on an NT host, and `crypto/x509`'s `hostRootPool` seam
(`root_hostpool_cosmo.go`, with a `return nil, false` beside it for every
other port) parses what it gets. `SSL_CERT_FILE` and `SSL_CERT_DIR` still
outrank it.

That is the store, not NT's chain engine: a server omitting an intermediate
still fails here and still succeeds under the engine, which fetches one.
`certs_cosmo_nt_test.go` pins the CERT_CONTEXT offsets, for the reason
`dns_cosmo_nt_test.go` pins the FIXED_INFO ones - a wrong offset reads
plausible garbage rather than failing - and `testdata/runtimeprobe/tls.go`
measures a real handshake on every runner.

## The cosmo test tree has more of these, and one is fixed here

A `_linux.go` filename matches cosmo, so upstream's linux-only test files
compile under it and reach for syscalls cosmo does not have.

`crypto/x509`'s `root_linux_test.go` was one: `syscall.Mount`, `Unmount`,
`CLONE_NEWNS` and `CLONE_NEWUSER` are all undefined, so
`GOOS=cosmo go vet crypto/x509` failed to build the test package. It now
carries `linux && !cosmo`, matching the `root_linux.go` it tests.

`runtime/testdata/testprog` is the other, and it is left: `segv_linux.go`
and `syscalls_linux.go` use `syscall.Tgkill`, `syscall.Unshare` and
`syscall.CLONE_FS`, so every runtime test that launches that helper fails
to build it - 118 of them, which is what
`go tool dist test -run '^runtime$'` reports under cosmo today. CI does not
see it because it names individual tests. Excluding the files turns a build
error into an unknown-command error for the modes they register, so the
repair is a cosmo variant per mode, and what each mode means on a Windows
or macOS host is its own decision.

# 2026-08-30: NT - os.PathListSeparator was compiled in, so PATH could not be built

`os.PathListSeparator` is an untyped constant on every upstream port, because a
port knows its host at compile time. A fat APE does not. `path_unix.go` covered
cosmo, so every APE carried the unix colon and used it on Windows too.

The 2026-07-20 `exec.LookPath` work fixed READING an NT PATH inside `os/exec`,
which is why this survived: only code outside that package saw the wrong value.
The field report was go-toolchain JOINING one - it prepends the fork's `bin` to
PATH before spawning the compiler, and on NT the colon fused the fork's
directory with the next entry into a path that does not exist. The stock Go that
`actions/setup-go` had installed won the lookup, and the build died with
`compile: version "go1.27.0cosmo.r705" does not match go tool version
"go1.27.0"` - a version-skew message with nothing about paths in it.

The fix: `src/os/path_cosmo.go` declares `PathListSeparator` as a `var`, read
once at package initialization from `runtime.CosmoHostOS()`. The entry stub
records the host before any Go code runs, so the value is available that early.
`PathSeparator` stays a constant slash - NT accepts it beside the backslash.

What that costs, and what it does not:

- `PathListSeparator` is now a `rune` rather than an untyped constant, so a
  comparison against a byte needs a conversion. Exactly one caller does that,
  `splitPathList` in `os/executable_cosmo.go`.
- `path/filepath.ListSeparator` cannot be a constant when the one it copies is
  not, so it splits by build tag: `listseparator_cosmo.go` holds the var,
  `listseparator_notcosmo.go` the const. `Separator` stays in `path.go`.
  Non-cosmo ports are unchanged.
- `cmd/api` does not include cosmo in its contexts, so there is no api/go1.txt
  breakage.
- `internal/filepathlite` keeps its own colon. `filepath.SplitList` reads
  `filepath`'s value, not that one.
- `SplitList` follows the host too, in `splitlist_cosmo.go`. Splitting on the
  right character is only half of it: an NT entry may be quoted, and a quoted
  entry may hold the separator itself, so `path_unix.go`'s plain
  `strings.Split` cuts inside one. The quote-aware body is `path_windows.go`'s,
  factored as `splitListQuoted(path, sep)` - the separator is a parameter so a
  linux host can still test the NT branch, which is what
  `TestSplitListQuoted` does with `path_test.go`'s own `winsplitlisttests`
  cases. `splitList` moved out of `path_unix.go` into `splitlist_notcosmo.go`;
  non-cosmo ports compile the same function they always did.

Gate: `checkLookPath` in testdata/runtimeprobe now asserts
`os.PathListSeparator` against its own NT oracle (the `OS=Windows_NT`
environment variable, independent of `runtime.CosmoHostOS()`) and round-trips
`filepath.SplitList`. Negative control on linux: `OS=Windows_NT ./runtimeprobe`
reports `FAIL lookpath: os.PathListSeparator = ":", host wants ";"`. CI runs
this probe on all three hosts.

## The os and syscall test binaries never built under cosmo

Found while doing the above, and fixed here. Neither package's tests compiled
for GOOS=cosmo, so nothing in either had ever run on this port.

The fork tags its non-test files honestly - `zero_copy_linux.go`,
`pidfd_linux.go` and the rest carry `linux && !cosmo`, because cosmo takes
`zero_copy_stub.go` and `pidfd_other.go` instead. The TEST files were left on
the bare `_linux` filename, which the fork resolves for cosmo too, so each one
referenced hooks that do not exist there. The repair is to give every test file
the tag of the implementation it exercises:

- `os`: `export_linux_test.go`, `readfrom_linux_test.go`, `writeto_linux_test.go`
  and `pidfd_linux_test.go` gain `linux && !cosmo`; `readfrom_unix_test.go`
  narrows to `(freebsd || linux || solaris) && !cosmo`.
- `syscall`: `export_linux_test.go` (which also collided outright with the
  fork's own `export_cosmo_test.go` over `Tcgetpgrp`/`Tcsetpgrp`),
  `creds_test.go`, `exec_linux_test.go` and `syscall_linux_test.go` gain the
  same tag. Netlink, `AllThreadsSyscall`, mount flags, namespaces and
  `SCM_CREDENTIALS` are Linux kernel surface cosmo does not expose.

One real gap sat under that: `syscall.Mkfifo` was missing for cosmo, which is
what `os/fifo_test.go` needs. `Mknod` was already there, so it is the same
one-liner linux has - `Mknod(path, mode|S_IFIFO, 0)`.

`syscall` now passes under cosmo. `os` now BUILDS and RUNS, and doing so
surfaced its own defects, which are a separate piece of work and are NOT fixed
here:

- `TestRootConsistencyRename`, `TestRootMultiMkdir`, `TestRootMultiMkdirAllShallow`
  and `TestRootMultiRemove` fail on trailing-slash handling. `os.Root` and the
  plain `os` call disagree with each other on the SAME host, so it is cosmo's
  openat path, not the kernel's: `root.Mkdir("target/")` succeeds against a
  symlink-to-directory where `os.Mkdir(".../target/")` correctly answers
  `file exists`. That is `O_NOFOLLOW`/`O_DIRECTORY` behavior inside
  Cosmopolitan's openat, below Go.
- `TestExecutableDeleted` fails with `device or resource busy`. Running as
  root, APE staging binds the staged copy over the original path (see
  `docs/APE-STAGING.md`), so the running executable cannot be unlinked. That is
  the staging design rather than a defect, and the test's premise does not hold
  for an assimilated APE.

Neither is reachable from CI today - no leg runs `dist test os` under cosmo -
so this change turns nothing red. What it does is make both failures visible
and gateable for the first time.

## An NT path read as relative, because the path layer was compiled as unix

`internal/filepathlite` is where `IsAbs`, `IsPathSeparator` and `volumeNameLen`
decide what a path IS, and cosmo took `path_unix.go`. So on a Windows host every
path the OS handed the APE - drive letter, backslashes - read as relative with
no volume. A consumer's report:

```
internal error: go list returned non-absolute Package.Dir: C:\Users\runneradmin\.cache\go-toolchain\cosmo\v372\go\src\internal\goarch
```

That is x/tools calling `filepath.IsAbs` on a path the native `go list` printed.
Nothing was wrong with the path.

`path_cosmo.go` moves those predicates onto the host, the same way the separator
moved. `Separator` stays the slash - NT accepts it beside the backslash, and it
is what this package emits, so only what a path is READ as changes.

The NT bodies are `path_windows.go`'s: a drive letter, a UNC root, and the
device prefixes (`\\.\`, `\\?\`, `\??\`) with their fold comparison and their
rejection of a parent reference inside the volume.

Each NT predicate takes the host as a parameter (`ntIsAbs(path, nt)`), which is
what lets a linux host test the NT branch - `TestNTIsAbs`,
`TestNTVolumeNameLen` and `TestNTIsPathSeparator` in
`internal/filepathlite`, each also checked against the unix branch. Negative
control: asserting `C:\Users\...` is relative fails with
`NTIsAbs("C:\\Users\\runneradmin\\.cache", nt) = true, want false`.

Verified on linux/amd64: make.bash; std for cosmo amd64 and arm64, linux,
windows, darwin, plan9, js/wasm and wasip1/wasm; the go/build guardrail; dist
test of path/filepath, os/exec, syscall and cmd/internal/script, all under
GOOS=cosmo; `TestSplitListQuoted` with a negative control; and runtimeprobe
executed here, also with a negative control.

## EOF on a dead child's stdout pipe, and what it ruled OUT on macOS

`checkPipeEOF` (testdata/runtimeprobe/pipeeof.go) asserts that a child's stdout
pipe reaches EOF once the child exits. os/exec waits for that EOF inside `Wait`,
so a write end that escaped to another process parks the parent forever. An end
can only escape through a fork in the window inside `Start`, between the pipe's
creation and the parent's close - the window close-on-exec covers. The
arm64-apple variadic ABI defect once left `FD_CLOEXEC` unset on a share of all
descriptors, so this is the same family.

Each round forks a holder child concurrently with a piped child, then requires
the piped child's `Wait` to return. The holder outlives the piped child, so an
escaped descriptor wedges rather than merely delaying. On a wedge the check
prints the parent's descriptor census and the holder's, then releases the holder
and reports whether that alone unwedged the copy - which separates a lost
close-on-exec from a pipe the kernel never drained.

Negative control: a scratch build that hands the holder the write end through
`ExtraFiles` reports

```
FAIL pipeeof: round 0: the piped child's stdout never reached EOF; parent fds=[... 29:pipeE ...]
FAIL pipeeof: round 0 holder census: pid=19270 fds=[0:chr- 1:chr- 2:chr- 3:pipe-]
FAIL pipeeof: round 0 releasing the holder unwedged the copy (wait: <nil>)
```

The holder's `3:pipe-` is the escaped end, and the trailing `-` says it carried
no `FD_CLOEXEC`.

### What this ruled out

The check was written for go-toolchain's macOS leg, which times out in
`src/cmd` and `src/vet` at the 30s per-test cap with the parent inside
`gocommand.runCmdContext` -> `os/exec writerDescriptor` -> `io.Copy` -> `poll.(*FD).Read`. It reports `ok
pipeeof` on the macOS runner, on all three origin binaries, so an escaped write
end is NOT that failure.

Reading the whole dump rather than the copy goroutines says the same thing: the
`Cmd.Wait` goroutine is parked in `syscall.wait4`, so the child `go list` had
not exited. Both pipe copies were therefore waiting correctly, and the parent
was not at fault. The module cache was warm by then - the last `go: downloading`
line precedes the timeouts by minutes - so a cold fetch is not it either. What
remains to explain is why a `go list` child under the fork takes more than 30s
on that host when the same call takes under a second on linux. The check stays
as a regression guard on the descriptor path it does cover.

# 2026-09-02: the macOS metadata wave - every remaining darwin arm64 syscall

`docs/STUBS-INVENTORY.md` section 6 listed the syscalls that fell
through `syscall6SlowDarwin` to `darwinENOSYS`: everything to do with
file metadata and system information. `os.File.Sync`, `os.Truncate`,
`os.Chmod`, `os.Chtimes`, `os.Link`, `os.Symlink`, `syscall.Statfs`,
`syscall.Uname`, `syscall.Getrlimit` and the credential getters all
failed on macOS. The apetest path never called them, so CI stayed green
over the whole gap.

All of them are emulated now. The inventory carries the per-syscall
table; what is worth keeping here is the three things that shaped the
implementation.

## The nosplit budget decides where a conversion can live

The dispatch spine runs after `entersyscall`, so every handler on it is
`//go:nosplit` and a stack split there is a fatal "stack split at bad
time". That is a hard ceiling on a handler's frame, and Apple's
`struct statfs` is 2168 bytes with `struct utsname` at 1280 - neither
fits.

So those three do not build their buffer in the emulation. Package
`syscall` allocates the Apple-layout struct (ordinary Go, allocation
legal) and converts it, exactly as the 2026-07-21 cmsg work split
control-buffer repacking across the same boundary. The emulation then
only forwards a pointer.

That split has an obvious way to go wrong: a caller reaching
`SYS_STATFS` with a Linux `Statfs_t` would take a two-kilobyte write
into 120 bytes. The size travels as an argument and the emulation
refuses a buffer smaller than the Apple struct, so that caller gets
EINVAL. `darwinabi_cosmo_test.go` pins the struct sizes and offsets, and
the size guard is what makes the pin load-bearing rather than decorative.

## Three syscalls are not a forward, and one of them looks like one

- `getpriority` returns a value where -1 is legal, so "did it fail?"
  cannot be read off the return. errno is zeroed through Apple's
  `__error()` before the call and read back after - Apple's own man page
  prescribes exactly this. It also has to return `20-nice`, because the
  LINUX SYSCALL applies that bias (glibc removes it again) and a Linux
  host reports the biased number. The runtimeprobe `sysinfo` check reads
  `prio=20` on linux, which is the contract macOS must match.
- `prlimit64` has no Apple counterpart taking a pid, numbers resources
  differently above `RLIMIT_CORE` (Linux NOFILE 7 is Apple 8; MEMLOCK
  and NPROC swap), and uses `2^63-1` where Linux uses `~0` for
  "unlimited". Passing either sentinel through unrewritten turns an
  unlimited resource into a nonsense finite one.
- `sendfile` reverses Apple's first two arguments against Linux's,
  reports its count through a pointer that is filled even when the call
  fails, and never moves the file offset - so a Linux caller passing no
  offset needs an lseek before and after. errno must be read before that
  second lseek, or the fixup clobbers it.

## What the tests actually prove, and where

The translation tables are pure arithmetic over Apple's numbering, so
they moved into `darwinabi_cosmo.go` under a plain `cosmo` build tag
rather than an arm64 one. That is not tidying: cosmo package tests run
on the ubuntu CI leg, and an arm64-tagged table would have been a table
no CI run ever executed.

The syscalls themselves are covered by runtimeprobe's `fsmeta`,
`sysinfo` and `sendfile` checks, which apetest runs on all three
runners. The linux run is the ground truth - it pins the semantics the
darwin emulation has to reproduce - and the macOS run is what proves
the dlsym wiring behind it. On a Windows host the three checks report
what each step answered instead of failing, following `checkDupFile`:
the NT emulation has not brought this surface up, and a check that
fails there would say nothing new.

Negative control, run before trusting any of it: breaking one flag
mapping in `darwinMntFlagsToLinux` turned `TestDarwinStatfsToLinux` and
`TestDarwinMntFlagsToLinux` red with the exact wrong bit named
(`Flags = 0x7, want 0x403`).
