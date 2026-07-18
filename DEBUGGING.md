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
