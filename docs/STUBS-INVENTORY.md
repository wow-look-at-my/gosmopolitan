# Cosmo syscall-emulation stub inventory

This document catalogs stubbed-out or broken functionality in the cosmo syscall emulation and runtime. Each item lists the file, line, and the observed behavior. The goal is a complete, honest accounting of what "works" only by pretending to, what fails loudly, and what is structurally broken — so follow-up.

The finding this document was opened on: **the darwin (macOS) syscall emulation is incomplete. The amd64 darwin path is largely stubbed and structurally.

The arm64 half of that is now closed — Section 6 lists what the metadata wave implemented and the few calls Apple genuinely cannot serve. The amd64 half stands as written. The same wave closed the NT metadata syscalls Win32 can serve (Section 5a).

The same wave also closed the macOS-Intel SYSCALL TABLE (Section 6b) and fixed the error-detection bug underneath it (Section 4.1b).

**Scope note.** "Missing syscall" here means a call the host CAN serve that the emulation did not. What remains after this wave is not that:

- **macOS-Intel** is closed by that definition. The table, the errno convention and numbering, the netpoller, the CPU count, parking, nanosleep, thread creation and signal installation all landed (Sections 2, 3. Nothing there is verified: there is no Intel-mac CI runner, so all of it was read and reasoned about and never executed.
- **Windows/arm64** (Section 4.3) is a port, not a syscall table: every `nt*` stub is a Win32 call the amd64 side already makes in Go. Also no runner, and no APE boot path to reach it through.

Both remain Section 8 items. The difference from the work above is that neither can be *proven* here.

---

## 1. The Seek/ReadAt gap (FIXED on this branch)

**Before this branch:** on macOS (both arm64 and amd64), `os.File.Seek` and `os.File.ReadAt` returned `ENOSYS` ("function not implemented"). Linux and Windows already dispatched `pread`/`lseek` natively. The gap was macOS-only — but it was real and user-visible.

- arm64: `SYS_PREAD64`/`SYS_PWRITE64`/`SYS_LSEEK` were absent from the `syscall6SlowDarwin` switch in `src/internal/runtime/syscall/cosmo/syscall_cosmo_arm64.go`, so they fell through to the `darwinENOSYS` default.
- amd64: `SYS_PREAD64`/`SYS_PWRITE64`/`SYS_LSEEK` were absent from the darwin dispatch in `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s`, so they fell through to `darwin_enosys`.

**Fix (this branch):** added `Pread`/`Pwrite`/`Lseek` to `DarwinFns` (dlsym-resolved via the Syslib), handled the three syscalls in the arm64 slow path, and added the three XNU dispatch. Verified on macOS arm64: all three `Seek` whence values and `ReadAt` (mid-file, past-EOF, at-EOF) return correct results, 5/5 runs. A regression check (`seekreadat`) is wired into `testdata/runtimeprobe`, which the CI matrix runs on Linux, macOS, and Windows.

---

## 2. Silent "return success" stubs (most dangerous)

These pretend to succeed while doing nothing. A caller cannot tell the operation was skipped. These are the worst kind of stub.

| # | Location | Behavior |
|---|----------|----------|
| 1 | ~~`darwin_nanosleep`~~ (FIXED 2026-09-02) | Was: success (r1=0, errno=0) **without sleeping**, so every caller ran straight through its delay and could not tell. XNU has no nanosleep, but `select` with a timeout and no descriptors is a sleep — which is what the runtime's own `usleep` already does on this host. `rem` is filled for real: BSD `select` does not update its timeout, so the remainder is measured with `gettimeofday` either side and clamped at zero, because a caller looping on EINTR would otherwise sleep twice. (The old note here also claimed `time.Sleep` was affected. It was not: `time.Sleep` goes through the runtime's timers and `usleep`, whose darwin branch was already real. `syscall.Nanosleep` was the caller that got the lie.) Covered by `testdata/runtimeprobe`'s `nanosleep` check, which asserts on the CLOCK — an error-only check passes against a stub that never sleeps. |
| 2 | ~~`darwin_sigaction`~~ (FIXED 2026-09-02) | Was: success **without installing the signal handler**, so `syscall.RawSyscall(SYS_RT_SIGACTION, ...)` on macOS-Intel did nothing and said it had worked. The asm branch now calls `darwinSigaction` in `sigaction_cosmo_amd64.go`, which translates the Linux `sigactiont` both ways — signal number, 8-byte-to-4-byte sigset remap, and the three flags that cross over — and issues the raw `__sigaction` syscall (BSD 46) over Apple's kernel struct. A real handler gets `sigactionTramp` as its `sa_tramp`: the kernel enters that with the handler, an infostyle token and the signal arguments, so it translates the signal number back to Linux numbering, calls the handler with (sig, info, ctx), and issues `sigreturn` (BSD 184). `runtime·cosmoXnuSigtramp` cannot serve here — it drops the handler and dispatches through `runtime·sigtramp`, which is right for the runtime's own handlers and wrong for a caller's. `siginfo` and the `ucontext` keep their Apple layouts; only the signal number can be made to match what the caller installed under. A signal with no Apple number (SIGSTKFLT, SIGPWR, the realtime range) reports EINVAL rather than a handler this host can never deliver. ABI source: Go's pre-1.12 darwin port (go1.8 `runtime/defs_darwin_amd64.go`). Still unverified: there is no Intel-mac runner. |
| 3 | ~~`rt_sigaction` darwin branch~~ (FIXED 2026-09-02) | Was: success without calling sigaction at all, so every handler the runtime believed it had installed was absent. `sysSigaction` lost its `GOARCH == "arm64"` guard and routes both arches through `darwinSigaction`; on amd64 that is `signal_cosmo_xnu_amd64.go`, which translates the Linux `sigactiont` both ways and issues the raw `__sigaction` syscall (BSD 46). The kernel struct carries an `sa_tramp` field the libc struct does not — libc fills it with its own trampoline, so a raw caller must supply one, and `runtime·cosmoXnuSigtramp` (`sys_cosmo_amd64.s`) is it: the kernel enters it with the handler, an infostyle token and the signal arguments, it reshuffles onto `runtime·sigtramp`'s (sig, info, ctx) contract, and it calls `sigreturn` (BSD 184) when the handler returns. The ABI source is Go's own pre-1.12 darwin port (go1.8 `runtime/sys_darwin_amd64.s`, `defs_darwin_amd64.go`), the same source `bsdthread_create` came from. The asm darwin branch is now a crash poke, reached only if a new caller enters the assembly directly. Still unverified: there is no Intel-mac runner. |
| 4 | `src/runtime/sys_cosmo_amd64.s` (`rt_sigaction` NT branch) | Was: success on NT wave 1, with no signal machinery. The comment called it "the same benign lie the darwin stub above tells". FIXED on the lock/ioctl branch, NOT yet confirmed: `sysSigaction` routes NT to `ntSigaction`, which records the handler, so the asm branch is unreachable and now crashes rather than lies. This row stays until a green run proves it. |
| 5 | `src/runtime/sys_cosmo_amd64.s` (`rtsigprocmask_nt`) | Was: success on NT, while blocking nothing. A critical section that had masked every signal could still be reentered by one. FIXED on the lock/ioctl branch, NOT yet confirmed: the runtime keeps its own mask (`ntSigMask`), a blocked signal waits pending instead of being delivered or dropped, and an unblock delivers what waited. This row stays until a green run proves it. |
| 6 | ~~`rtsigprocmask` darwin branch~~ (FIXED 2026-09-02) | Was: translated `how` (Linux 0/1/2 to Apple 1/2/3) but passed the mask through untouched, so the 8-byte Linux sigset reached a kernel expecting a 4-byte Apple one and every mask it set named the wrong signals. `sigprocmask` lost its `GOARCH == "arm64"` guard, so both arches route through `darwinSigprocmask`; on amd64 that is `signal_cosmo_xnu_amd64.go`, which remaps the set in both directions through the same `cosmoSigmaskL2A`/`cosmoSigmaskA2L` arm64 uses and issues the raw syscall (BSD 48). It crashes on failure, as the Linux asm path does. The asm darwin branch is now a crash poke. Still unverified: there is no Intel-mac runner. |
| 7 | ~~`src/syscall/syscall_cosmo_arm64.go` (`Getpgrp`)~~ | Was: calls `Getpgid(0)` and discards the error, so an ENOSYS `Getpgid` on macOS silently returned 0. `Getpgid` is emulated now (Section 6), and `getpgid(0)` cannot fail; the discard matches what the linux port does. The runtimeprobe `sysinfo` check asserts `Getpgrp() == Getpgid(0)` and that both are positive, so a regression to the silent zero fails CI. |

---

## 3. ENOSYS stubs (visible failure, functionality missing)

These fail loudly with `ENOSYS`. They are honest — but the functionality is missing.

| # | Location | Behavior |
|---|----------|----------|
| 1 | ~~`futex` darwin (amd64)~~ | The asm stub still answers ENOSYS, but nothing reaches it: `futexsleep`/`futexwakeup` branch on `isdarwin()` first (`os_cosmo.go`). XNU has no futex, and the primitives closest to one — `__ulock_wait` and the psynch family — are not in this tree's syscall table, so their numbers would have to be guessed. The wait is a poll of the word with a 20µs→5ms backoff instead, which the futex contract permits: a sleeper may wake spuriously, and it observes `*addr` leaving `val` on its own. The wake is therefore a no-op. Cost is latency, bounded by 5ms. **This also removed a live crash**: the ENOSYS used to fall through to `futexwakeup`'s crash poke on the first contended unlock on a macOS-Intel host. |
| 2 | ~~`clone` darwin (amd64)~~ (FIXED 2026-09-02) | Was: ENOSYS, so `newosproc` threw and macOS-Intel could not start a second thread. XNU has no clone; it has `bsdthread_create` (360), which is exactly how **Go's own darwin port created threads until it moved to libc in Go 1.12**. That port is the ABI source — not a reconstruction: `PTHREAD_START_CUSTOM` (0x01000000), registration through `bsdthread_register` (366) at `osArchInit`, the kernel entering the new thread with `DX`=arg1, `CX`=arg2, `R8`=stack, and `bsdthread_terminate` (361) if the entry returns. Only two values reach the child, so `gp` is not passed: `newosproc` always passes `mp.g0` and the stub derives it from the m, as Go did; `newosproc0`'s nil m takes the same path the Linux child's `nog2` does. |
| 2b | ~~`settls` darwin (amd64)~~ (FIXED 2026-09-02) | Was: `RET` — "TLS may need different handling". A silent no-op, so any darwin thread read `g` from whatever the GS base happened to point at. Nothing noticed while `clone` was ENOSYS and no second thread existed; it became load-bearing the moment one could. Now the `thread_fast_set_cthread_self` machdep call (0x3000003) with `&m.tls[0]-0x28`, so `gs:0x28` lands on `m.tls[0]` exactly as the Linux path arranges. The kernel stores the value it is handed as the GS base unchanged (`machine_thread_set_tsd_base`); the `0x8a0` go1.8 subtracted was that linker's own TLS offset, and applying it here pointed `gs:0x28` 0x8a0 bytes below the g slot. |
| 3 | ~~`futex` darwin (arm64)~~ | Unreachable: cosmo/arm64 builds `lock_sema.go`, not `lock_futex.go` (see the build tags), so an M parks on the Syslib's pthread condition variables (`os_cosmo_arm64_sema.go`) and `futexsleep`/`futexwakeup` branch on `isdarwin()` first. The branch is a crash poke (0xf9) rather than an ENOSYS, so a caller that finds a hole says so instead of passing. |
| 3b | `src/runtime/sys_cosmo_arm64.s` (`madvise` darwin) | Was: 0 (success) without advising anything, so `MADV_DONTNEED` and `MADV_FREE` never reached the kernel and the heap kept every page it had touched. FIXED on the lock/ioctl branch, NOT yet confirmed: `osArchInit` resolves Apple's madvise through dlsym. The advice number is translated, because the two systems agree only up to 4 — Linux `MADV_FREE` is 8, which Apple numbers 5 and spends on `MADV_FREE_REUSE`. Linux-only advice fails rather than naming an unrelated Apple one. This row stays until a green run proves it. |
| 4 | `src/runtime/sys_cosmo_arm64.s` (`mincore` darwin) | Was: -1 always, because mincore is not in the Syslib. Its one caller is the page-size probe in `sysauxv`, which reads a failure as "try the next size". So every probe failed and `physPageSize` fell back to 256K. FIXED on the lock/ioctl branch, NOT yet confirmed: `osArchInit` resolves Apple's mincore through dlsym, like getpid. This row stays until a green run proves it. |
| 5 | ~~`cosmoDarwinKqueue`/`cosmoDarwinKevent` on amd64~~ | Was: ENOSYS, on the reasoning that the poller needs Apple libc through a Syslib amd64 does not have. It needs no libc. `SYS_KQUEUE` (362) and `SYS_KEVENT` (363) are both in `syscall/zsysnum_darwin_amd64.go`, and `keventt` is already Apple's 64-bit `struct kevent` (`netpoll_cosmo_xnu.go`), shared with arm64. Both are served by raw XNU syscall now, and `cosmoDarwinKqueueSupported` reports true. |
| 6 | ~~`cosmoDarwinNumCPU` on amd64~~ | Was: 0 ("no Syslib, so no sysctl access"), so `getCPUCount` fell back to 1. The numeric MIB needs no name lookup: `SYS___SYSCTL` is 202 in the same table, and `_CTL_HW`/`_HW_NCPU` are 6/3 in `runtime/os_darwin.go`'s own `getCPUCount`. Reads hw.ncpu directly now. |
| 7 | ~~`darwinSigprocmask` on amd64~~ | Was: `throw("not implemented on amd64")`, a stub that existed so the amd64 build linked. Real now — `signal_cosmo_xnu_amd64.go`, see Section 2.6. |
| 8 | ~~`darwinSigaction` on amd64~~ | Was: -1, a stub that existed so the amd64 build linked. Real now — `signal_cosmo_xnu_amd64.go`, see Section 2.3. |

---

## 4. Structurally broken / untested paths

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s` (whole darwin path) | The amd64 darwin path issues **raw XNU `SYSCALL` instructions**. CLAUDE.md's blanket "macOS does not allow raw syscalls" is overbroad: that restriction is what ARM64 macOS enforces, which is why arm64 needs the Syslib and dlsym at all, and cosmopolitan itself issues raw syscalls on x86-64 XNU. The design is sound in principle, and as of 2026-09-02 the syscall surface behind it is complete — clone, futex, the netpoller and the rest all landed. **It is still unverified.** There is no Intel-mac CI runner, so nothing here has ever executed. Do not claim it works. |
| 1b | ~~same file, `darwin_error`~~ | The darwin return path tested for failure with the LINUX convention (`AX > -4096`). XNU signals failure with the CARRY FLAG and returns a POSITIVE errno, so every failing syscall was reported as SUCCESS carrying the errno as its result. Now `JCS`, followed by `runtime·cosmo_xlat_errno_ax` for the numbering. |
| 1c | `src/runtime/sys_cosmo_amd64.s` (FIXED 2026-09-02) | The identical carry-flag defect, in the runtime's own raw-XNU branches. `open` returned the errno as a file descriptor (`ENOENT` opened "fd 2"); `read`/`write1` returned it as a byte count, so a failed write read as a short write; `pipe2` produced a pair of fds out of it; `mmap` returned a mapping at address `ENOMEM` that every caller then wrote to. Each failing branch now tests the carry flag and, where the value reaches Go, translates the errno. `munmap` and `rtsigprocmask` keep their crash-on-failure pokes, now reached by `JCC`. |
| 2 | ~~`cosmoDarwinKqueueSupported` returns false~~ | Fixed with Section 3.5: the poller is served by raw XNU syscall. What is left on macOS-Intel is thread creation and parking, which is a narrower and more accurate claim than "the netpoller is unsupported". |
| 3 | `src/runtime/os_cosmo_nt_arm64.go` | **The Win32 layer is implemented on arm64; the boot path and the netpoller are not.** `sys_cosmo_nt_arm64.s` supplies the AAPCS64 `ntcall6`/`ntcall10` trampolines (TEB via `R18_PLATFORM`, the `SetLastError(0)` bracket), the thread start, the VEH/VCH thunks and the console/signal trampolines, and `os_cosmo_nt_ctx_arm64.go` supplies the `ARM64_NT_CONTEXT` record and the five operations preemption performs on it. Every file that was `cosmo && amd64` is now `cosmo`, so `ntFutexsleep`, `ntNewosproc`, `ntVirtualAlloc`/`Free`, `ntSigaction`, `ntGoenvs`, `ntPreemptM`, `ntMinitThread`, `ntInitConsoleCtrl` and the profiler are live code on both arches. Sourcing is upstream's own windows/arm64 port (`runtime/sys_windows_arm64.s`, `internal/runtime/syscall/windows/defs_windows_arm64.go`, `signal_windows_arm64.go`). Still throwing: `netpollinitNT` and friends, because they sit on the syscall-emulation layer, which is written against Linux **amd64** numbering — arm64 has no bare `stat`/`lstat`/`open`, its `struct stat` is 144 bytes rather than 128, and `O_DIRECTORY` differs. That split is its own job. **Still unreachable, and still enforced rather than asserted**: `iswindows` remains a constant `false` on arm64, because no APE boot stub sets `__hostos` there (`rt0_cosmo_nt_amd64.s` has no arm64 twin and the APE has no arm64 PE header), and `TestPlatformTableIsClosed` (`cmd/internal/cosmoape`, run unfiltered by the ubuntu leg) fails if a windows row for a non-amd64 arch is added to the linker's boot-mechanism table without the runtime to back it. |
| 4 | ~~`darwin_sigaction` (arm64)~~ | The branch forwarded `rt_sigaction`'s own arguments to Apple libc's `sigaction`, which wants an Apple signal number and a 16-byte struct rather than Linux's 32-byte one, so the number, the mask and every flag were wrong. It calls `darwinSigactionSyslib` (`sigaction_cosmo_arm64.go`) now, which translates both structs over the tables the amd64 side shares. |

---

## 5a. NT metadata syscalls (partly CLOSED — metadata wave, 2026-09-02)

The NT emulation already served fsync, ftruncate, fchmod, chdir and getcwd. The metadata wave added the four Win32 can serve and programs actually reach (`src/runtime/os_cosmo_nt_meta.go`):

| syscall | Win32 | notes |
|---|---|---|
| `utimensat` | `SetFileTime` | Win32's own convention IS the sentinel: a NULL stamp pointer means "leave alone", which is exactly UTIME_OMIT. UTIME_NOW is filled from `GetSystemTimeAsFileTime`. |
| `truncate` | `SetEndOfFile` | The path form of the ftruncate NT already had; NT resizes only through a handle, so it opens one. |
| `fchdir` | `GetFinalPathNameByHandleW` + `SetCurrentDirectoryW` | NT has no handle-relative chdir. The `\\?\` prefix is trimmed for the drive form, because SetCurrentDirectoryW rejects it. |
| `linkat` | `CreateHardLinkW` | Argument order is reversed against linkat. |

These four are resolved from kernel32 OPTIONALLY: a zero pointer answers ENOSYS at the use site rather than poking a crash address at boot.

**Still ENOSYS on NT**, because Windows has no counterpart and upstream's own windows port does not expose it either: `prlimit64`. `fchmod`/`fchmodat` remain a documented no-op after an existence check, and `fchown`/`fchownat` have no unix ownership to change. See the reasoning in `ntEmuFchmod`.

The runtimeprobe split follows exactly this line: `fsmeta` and `volume` are hard assertions on all three hosts, `fsmetaunix` and `sysinfo` report rather than fail on.


## 6. macOS arm64: the syscall gap (CLOSED — metadata wave, 2026-09-02)

The arm64 slow path (`syscall6SlowDarwin`) handles a fixed set of syscalls. Everything else returns `darwinENOSYS`. That set used to stop at the file-I/O, socket and process syscalls the apetest path reaches, so every metadata and system-information call fell through —.

All of them are emulated now, in `internal/runtime/syscall/cosmo/{file,proc,darwinabi}_cosmo*.go`. Most are fixed-arity Apple libc entries whose arguments and constants already agree, so they pass straight through `darwinCall`. The ones that do not:

| syscall | why it needed more than a forward |
|---|---|
| `getpriority` | Apple returns the nice value; the Linux syscall returns `20-nice`. -1 is a legal result, so errno is cleared first and read back. |
| `prlimit64` | Apple's getrlimit/setrlimit take no pid, number the resources differently above `RLIMIT_CORE`, and use a different infinity sentinel. |
| `utimensat` | `UTIME_NOW`/`UTIME_OMIT` are `(1<<30)-1`/`-2` on Linux and `-1`/`-2` on Apple. |
| `sendfile` | Apple takes the file first and the socket second, reports the count through a pointer (filled even on failure), and never moves the file offset. |
| `linkat`, `fchownat` | `AT_SYMLINK_FOLLOW`/`AT_SYMLINK_NOFOLLOW` have different values. |
| `mknodat` | Apple has no directory-relative form. Served for `AT_FDCWD` (what `Mknod`/`Mkfifo` pass) over `mknod`; any other dirfd is ENOSYS rather than a path silently resolved against the wrong directory. |
| `statfs`, `fstatfs`, `uname` | Apple's structs are 2168 and 1280 bytes, far past the nosplit budget the dispatch spine runs under. The Apple-layout buffer is allocated in package `syscall` (`bigbuf_cosmo*.go`) and converted there; the emulation refuses a buffer smaller than the Apple struct, so a caller that reached `SYS_STATFS` with a Linux `Statfs_t` gets EINVAL instead of a two-kilobyte overrun. |

Two Linux `Statfs_t` fields have no Apple source. `Type` carries Apple's own filesystem-type number (the choice `Stat_t.Dev` already makes for device numbers) and `Namelen` stays zero rather than carrying a guess. `Utsname.Domainname` stays empty for the same reason.

**Still ENOSYS on macOS, because Apple has no counterpart:** `Setresuid`, `Setresgid`, `Setfsuid`, `Setfsgid`, and `mknodat` with a directory descriptor. `Fchmodat` reports `EOPNOTSUPP` for `AT_SYMLINK_NOFOLLOW` on every host: the Linux syscall takes no flags, and one APE answering the same call differently per host.

**Coverage.** The pure translation tables are unit-tested where cosmo tests run (`darwinabi_cosmo_test.go`, `bigbuf_cosmo_test.go`, both on the ubuntu CI leg). The syscalls themselves are exercised end to end by `testdata/runtimeprobe`'s `fsmeta`, `sysinfo` and `sendfile` checks, which the apetest suite runs on all three. Those checks report rather than fail on a Windows host, following `checkDupFile`: the NT emulation has not brought this surface up, and the log.

---

## 6b. macOS-Intel: the syscall table (CLOSED — metadata wave, 2026-09-02)

The amd64 darwin dispatch carried 18 syscalls and answered everything else ENOSYS. It now also serves fsync, truncate, ftruncate, fchmod, fchown, fchdir, chroot, get/setgroups, get/setpriority, setuid, setgid, setreuid, setregid, getpgid, and statfs/fstatfs (the last two through XNU's statfs64/fstatfs64, with the same buffer-size guard the arm64 path applies, so a Linux-layout buffer is refused rather than overrun). `getpriority` applies the Linux `20-nice` bias, which the carry-flag convention makes unambiguous — the value alone cannot say whether the call failed.

Failures now report LINUX errno numbers. `runtime·cosmo_xlat_errno_ax` (`sys_cosmo_amd64.s`) is the amd64 twin of arm64's `cosmo_xlat_errno_r0`, over the one shared table in `sys_cosmo_errno.s`. A linkname push makes it reachable from. An earlier note here claimed that cannot be done. It was reasoning about the DATA table rather than the function that wraps it.

**Every BSD number came from `syscall/zsysnum_darwin_amd64.go`**, the tree's own authority, not from memory. That mattered: statfs64 is 345, not the 338 recall suggested. Anything that file does not carry is deliberately absent rather than guessed — a wrong syscall number does not fail, it calls a DIFFERENT syscall. That is why the *at family (`linkat`, `symlinkat`, `fchmodat`, `fchownat`) and `utimensat` stay ENOSYS on amd64 while arm64 serves them: arm64 resolves by NAME through dlsym and. `uname` stays ENOSYS for a different reason — XNU has no uname syscall at all. It is a libc function over sysctl. `sendfile` stays ENOSYS because Apple's argument order, its value-result count pointer and its untouched file offset need real control flow, which the arm64 Go.

This closes the syscall TABLE. It does not bring up macOS-Intel: see Section 4.1.

## 7. What this means

- The macOS arm64 syscall surface serves everything the `syscall` package exposes and Apple can serve, termios ioctls included. Before writing "complete" here, diff every `SYS_*` the cosmo `syscall` package names against the dispatch. The termios round trip has never run against a live terminal: no CI runner has one.
- The "return success" stubs (Section 2) are still the highest-risk items: they hide failures.
- The macOS-Intel (amd64) SYSCALL surface is closed: the table, the error convention, the errno numbering, the netpoller, the CPU count, parking, thread creation, TLS. So are signals in the runtime: `darwinSigaction` issues a real `__sigaction` with its own trampoline (Section 2.3), and `darwinSigprocmask` bridges the sigset width (Section 2.6). The `syscall` package's own `rt_sigaction` emulation installs handlers for real too (Section 2.2). None of it is verified, because there is no Intel-mac runner — read and reasoned about, never executed.
- Windows/arm64 (Section 4.3) has its Win32 layer now — trampolines, thread start, exception dispatch, preemption context. It is still unreachable (no APE boot path, no platform token), and its netpoller still throws, waiting on an arm64 split of the syscall emulation.

## 8. Recommended follow-up

1. ~~Implement the remaining darwin arm64 syscalls in `syscall6SlowDarwin`.~~ Done. See Section 6.
2. ~~Replace the remaining amd64 darwin "return success" stub, `darwin_sigaction` in `internal/runtime/syscall/cosmo`.~~ Done. See Section 2.2.
3. **The macOS-Intel syscall surface is closed.** Every item once filed under this heading is done: the metadata table (Section 6b), the error convention and errno. Signals in the runtime are done as well: a real `__sigaction` with a translated struct and its own trampoline (Section 2.3), and a mask path. The `syscall` package's `rt_sigaction` emulation is done too (item 2).

   None of it is verified. There is no Intel-mac runner, so every line of the above has been read and reasoned about but never executed. That remains the honest blocker on the platform, and `Default()` leaves darwin/amd64 out of a default build for exactly this reason.
4. Windows/arm64: the `ntcall` trampolines, the `CONTEXT` layout and the VEH thunks are written (Section 4.3), sourced from upstream's own windows/arm64 port. Two things are left. The netpoller needs the syscall emulation split per architecture — numbers, `struct stat` width and `O_DIRECTORY` all differ. And the boot mechanism does not exist: the linker has no way to emit an arm64 PE header, so nothing sets `__hostos` there. Until it does, none of this code can be started, let alone tested, and `iswindows` stays a constant `false` so the compiler deletes it.
5. Exercise the termios conversion against a real pty. The tables are unit-tested and the syscall reaches the kernel, but no CI runner has a terminal, so the round trip has never run against a live driver.
6. Give `sendfile` an in-tree consumer. `internal/poll/sendfile_unix.go` carries no cosmo build tag, so `io.Copy` from a file to a socket never reaches the syscall on any cosmo host. Only a caller that uses `syscall.Sendfile` directly does. Adding cosmo to that tag needs the NT emulation to serve sendfile too, which it does not yet.
