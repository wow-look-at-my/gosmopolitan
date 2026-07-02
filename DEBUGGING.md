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

**Probe** (now 30 checks): + sleep (wall-clock bounded), ticker, after,
ctxtimeout, tcplisten/tcpecho/tcpserver, deadline (read deadline in the
past against a held-open conn -> i/o timeout), udp (loopback datagram),
execchild (self-exec through the full os/exec stack, launch mode chosen
by binary magic: ELF/Mach-O/windows direct, pristine APE via /bin/sh),
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
addr round-trip + unnamed-dialer canary; Windows expects "@" there,
per net's own unixsock tests) and unixecho - which also gives the
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
