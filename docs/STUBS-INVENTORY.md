# Cosmo syscall-emulation stub inventory

This document catalogs stubbed-out or broken functionality in the cosmo syscall emulation and runtime. Each item lists the file, line, and the observed behavior. The goal is a complete, honest accounting of what "works" only by pretending to, what fails loudly, and what is structurally broken — so follow-up work can be triaged.

The finding this document was opened on: **the darwin (macOS) syscall emulation is incomplete, and the amd64 darwin path is largely stubbed and structurally broken.** `Seek`/`ReadAt` were one instance of a broader pattern, not an isolated bug.

The arm64 half of that is now closed — Section 6 lists what the metadata wave implemented and the few calls Apple genuinely cannot serve. The amd64 half stands as written. The same wave closed the NT metadata syscalls Win32 can serve (Section 5a).

The same wave also closed the macOS-Intel SYSCALL TABLE (Section 6b) and fixed the error-detection bug underneath it (Section 4.1b).

**Scope note.** "Missing syscall" here means a call the host CAN serve that the emulation did not. What remains after this wave is not that:

- **macOS-Intel** is closed by that definition. The table, the errno convention and numbering, the netpoller, the CPU count, parking, nanosleep, thread creation and signal installation all landed (Sections 2, 3, 4 and 6b), and so did signal DELIVERY: `sigctxt` reads the XNU `user_ucontext64`/`x86_thread_state64`/`user64_siginfo` layouts on darwin hosts, the trampoline translates the Apple signal number and passes the sigreturn token back, `sigaltstack` translates Apple's `stack_t`, `raise`/`kill`/`tgkill` send Apple numbers, and `tgkill` is `__pthread_kill` on the thread's mach port. Nothing there is verified: there is no Intel-mac CI runner, so all of it was read and reasoned about and never executed.
- **Windows/arm64** (Section 4.3) is a port, not a syscall table: every `nt*` stub is a Win32 call the amd64 side already makes in Go. Also no runner, and no APE boot path to reach it through.

Both remain Section 8 items. The difference from the work above is that neither can be *proven* here.

---

## 1. The Seek/ReadAt gap (FIXED on this branch)

**Before this branch:** on macOS (both arm64 and amd64), `os.File.Seek` and `os.File.ReadAt` returned `ENOSYS` ("function not implemented"). Linux and Windows already dispatched `pread`/`lseek` natively, so the gap was macOS-only — but it was real and user-visible.

- arm64: `SYS_PREAD64`/`SYS_PWRITE64`/`SYS_LSEEK` were absent from the `syscall6SlowDarwin` switch in `src/internal/runtime/syscall/cosmo/syscall_cosmo_arm64.go`, so they fell through to the `darwinENOSYS` default.
- amd64: `SYS_PREAD64`/`SYS_PWRITE64`/`SYS_LSEEK` were absent from the darwin dispatch in `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s`, so they fell through to `darwin_enosys`.

**Fix (this branch):** added `Pread`/`Pwrite`/`Lseek` to `DarwinFns` (dlsym-resolved via the Syslib), handled the three syscalls in the arm64 slow path, and added the three XNU dispatch entries to the amd64 asm. Verified on macOS arm64: all three `Seek` whence values and `ReadAt` (mid-file, past-EOF, at-EOF) return correct results, 5/5 runs. A regression check (`seekreadat`) is wired into `testdata/runtimeprobe`, which the CI matrix runs on Linux, macOS, and Windows.

---

## 2. Silent "return success" stubs (most dangerous)

These pretend to succeed while doing nothing. A caller cannot tell the operation was skipped. These are the worst kind of stub.

| # | Location | Behavior |
|---|----------|----------|
| 1 | ~~`darwin_nanosleep`~~ (FIXED 2026-09-02) | Was: success (r1=0, errno=0) **without sleeping**, so every caller ran straight through its delay and could not tell. XNU has no nanosleep, but `select` with a timeout and no descriptors is a sleep — which is what the runtime's own `usleep` already does on this host. `rem` is filled for real: BSD `select` does not update its timeout, so the remainder is measured with `gettimeofday` either side and clamped at zero, because a caller looping on EINTR would otherwise sleep twice. (The old note here also claimed `time.Sleep` was affected. It was not: `time.Sleep` goes through the runtime's timers and `usleep`, whose darwin branch was already real. `syscall.Nanosleep` was the caller that got the lie.) Covered by `testdata/runtimeprobe`'s `nanosleep` check, which asserts on the CLOCK — an error-only check passes against a stub that never sleeps. |
| 2 | ~~`darwin_sigaction`~~ (FIXED 2026-09-02) | Was: success **without installing the signal handler**, so `syscall.RawSyscall(SYS_RT_SIGACTION, ...)` on macOS-Intel did nothing and said it had worked. The asm branch now calls `darwinSigaction` in `sigaction_cosmo_amd64.go`, which translates the Linux `sigactiont` both ways — signal number, 8-byte-to-4-byte sigset remap, and the three flags that cross over — and issues the raw `__sigaction` syscall (BSD 46) over Apple's kernel struct. A real handler gets `sigactionTramp` as its `sa_tramp`: the kernel enters that with the handler, an infostyle token and the signal arguments, so it translates the signal number back to Linux numbering, calls the handler with (sig, info, ctx), and issues `sigreturn` (BSD 184). `runtime·cosmoXnuSigtramp` cannot serve here — it drops the handler and dispatches through `runtime·sigtramp`, which is right for the runtime's own handlers and wrong for a caller's. `siginfo` and the `ucontext` keep their Apple layouts; only the signal number can be made to match what the caller installed under. A signal with no Apple number (SIGSTKFLT, SIGPWR, the realtime range) reports EINVAL rather than a handler this host can never deliver. ABI source: Go's pre-1.12 darwin port (go1.8 `runtime/defs_darwin_amd64.go`). Still unverified: there is no Intel-mac runner. |
| 3 | ~~`rt_sigaction` darwin branch~~ (FIXED 2026-09-02) | Was: success without calling sigaction at all, so every handler the runtime believed it had installed was absent. `sysSigaction` lost its `GOARCH == "arm64"` guard and routes both arches through `darwinSigaction`; on amd64 that is `signal_cosmo_xnu_amd64.go`, which translates the Linux `sigactiont` both ways and issues the raw `__sigaction` syscall (BSD 46). The kernel struct carries an `sa_tramp` field the libc struct does not — libc fills it with its own trampoline, so a raw caller must supply one, and `runtime·cosmoXnuSigtramp` (`sys_cosmo_amd64.s`) is it: the kernel enters it with the handler, an infostyle token and the signal arguments, it reshuffles onto `runtime·sigtramp`'s (sig, info, ctx) contract, and it calls `sigreturn` (BSD 184) when the handler returns. The ABI source is Go's own pre-1.12 darwin port (go1.8 `runtime/sys_darwin_amd64.s`, `defs_darwin_amd64.go`), the same source `bsdthread_create` came from. The asm darwin branch is now a crash poke, reached only if a new caller enters the assembly directly. Still unverified: there is no Intel-mac runner. |
| 4 | `src/runtime/sys_cosmo_amd64.s:623-625` (`rt_sigaction` NT branch) | Returns success on NT wave 1 (no signal machinery). Comment: "the same benign lie the darwin stub above tells". |
| 5 | `src/runtime/sys_cosmo_amd64.s:601` (`rtsigprocmask_nt`) | Returns success on NT (no signal machinery). |
| 6 | ~~`rtsigprocmask` darwin branch~~ (FIXED 2026-09-02) | Was: translated `how` (Linux 0/1/2 to Apple 1/2/3) but passed the mask through untouched, so the 8-byte Linux sigset reached a kernel expecting a 4-byte Apple one and every mask it set named the wrong signals. `sigprocmask` lost its `GOARCH == "arm64"` guard, so both arches route through `darwinSigprocmask`; on amd64 that is `signal_cosmo_xnu_amd64.go`, which remaps the set in both directions through the same `cosmoSigmaskL2A`/`cosmoSigmaskA2L` arm64 uses and issues the raw syscall (BSD 48). It crashes on failure, as the Linux asm path does. The asm darwin branch is now a crash poke. Still unverified: there is no Intel-mac runner. |
| 7 | ~~`src/syscall/syscall_cosmo_arm64.go` (`Getpgrp`)~~ | Was: calls `Getpgid(0)` and discards the error, so an ENOSYS `Getpgid` on macOS silently returned 0. `Getpgid` is emulated now (Section 6), and `getpgid(0)` cannot fail; the discard matches what the linux port does. The runtimeprobe `sysinfo` check asserts `Getpgrp() == Getpgid(0)` and that both are positive, so a regression to the silent zero fails CI. |

---

## 3. ENOSYS stubs (visible failure, functionality missing)

These fail loudly with `ENOSYS`, so they are honest — but the functionality is missing.

| # | Location | Behavior |
|---|----------|----------|
| 1 | ~~`futex` darwin (amd64)~~ | The asm stub still answers ENOSYS, but nothing reaches it: `futexsleep`/`futexwakeup` branch on `isdarwin()` first (`os_cosmo.go`). XNU has no futex, and the primitives closest to one — `__ulock_wait` and the psynch family — are not in this tree's syscall table, so their numbers would have to be guessed. The wait is a poll of the word with a 20µs→5ms backoff instead, which the futex contract permits: a sleeper may wake spuriously, and it observes `*addr` leaving `val` on its own. The wake is therefore a no-op. Cost is latency, bounded by 5ms. **This also removed a live crash**: the ENOSYS used to fall through to `futexwakeup`'s crash poke on the first contended unlock on a macOS-Intel host. |
| 2 | ~~`clone` darwin (amd64)~~ (FIXED 2026-09-02) | Was: ENOSYS, so `newosproc` threw and macOS-Intel could not start a second thread. XNU has no clone; it has `bsdthread_create` (360), which is exactly how **Go's own darwin port created threads until it moved to libc in Go 1.12**. That port is the ABI source — not a reconstruction: `PTHREAD_START_CUSTOM` (0x01000000), registration through `bsdthread_register` (366) at `osArchInit`, the kernel entering the new thread with `DX`=arg1, `CX`=arg2, `R8`=stack, and `bsdthread_terminate` (361) if the entry returns. Only two values reach the child, so `gp` is not passed: `newosproc` always passes `mp.g0` and the stub derives it from the m, as Go did; `newosproc0`'s nil m takes the same path the Linux child's `nog2` does. |
| 2b | ~~`settls` darwin (amd64)~~ (FIXED 2026-09-02) | Was: `RET` — "TLS may need different handling". A silent no-op, so any darwin thread read `g` from whatever the GS base happened to point at. Nothing noticed while `clone` was ENOSYS and no second thread existed; it became load-bearing the moment one could. Now the `thread_fast_set_cthread_self` machdep call (0x3000003) with `&m.tls[0]-0x28`, so `gs:0x28` lands on `m.tls[0]` exactly as the Linux path arranges. The kernel stores the value it is handed as the GS base unchanged (`machine_thread_set_tsd_base`); the `0x8a0` go1.8 subtracted was that linker's own TLS offset, and applying it here pointed `gs:0x28` 0x8a0 bytes below the g slot. |
| 3 | `src/runtime/sys_cosmo_arm64.s:1099-1101` (`futex` darwin) | Returns ENOSYS, and is unreachable: cosmo/arm64 builds `lock_sema.go`, not `lock_futex.go` (see the build tags), so it parks on the Syslib's pthread condition variables (`os_cosmo_arm64_sema.go`). That split is why macOS arm64 never hit the crash item 1 describes. |
| 4 | `src/runtime/sys_cosmo_arm64.s:654-655` (`mincore` darwin) | Returns -1 (ENOSYS). "mincore not in Syslib". |
| 5 | ~~`cosmoDarwinKqueue`/`cosmoDarwinKevent` on amd64~~ | Was: ENOSYS, on the reasoning that the poller needs Apple libc through a Syslib amd64 does not have. It needs no libc. `SYS_KQUEUE` (362) and `SYS_KEVENT` (363) are both in `syscall/zsysnum_darwin_amd64.go`, and `keventt` is already Apple's 64-bit `struct kevent` (`netpoll_cosmo_xnu.go`), shared with arm64. Both are served by raw XNU syscall now, and `cosmoDarwinKqueueSupported` reports true. |
| 6 | ~~`cosmoDarwinNumCPU` on amd64~~ | Was: 0 ("no Syslib, so no sysctl access"), so `getCPUCount` fell back to 1. The numeric MIB needs no name lookup: `SYS___SYSCTL` is 202 in the same table, and `_CTL_HW`/`_HW_NCPU` are 6/3 in `runtime/os_darwin.go`'s own `getCPUCount`. Reads hw.ncpu directly now. |
| 7 | ~~`darwinSigprocmask` on amd64~~ | Was: `throw("not implemented on amd64")`, a stub that existed so the amd64 build linked. Real now — `signal_cosmo_xnu_amd64.go`, see Section 2.6. |
| 8 | ~~`darwinSigaction` on amd64~~ | Was: -1, a stub that existed so the amd64 build linked. Real now — `signal_cosmo_xnu_amd64.go`, see Section 2.3. |

---

## 4. Structurally broken / untested paths

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s` (whole darwin path) | The amd64 darwin path issues **raw XNU `SYSCALL` instructions**. CLAUDE.md's blanket "macOS does not allow raw syscalls" is overbroad: that restriction is what ARM64 macOS enforces, which is why arm64 needs the Syslib and dlsym at all, and cosmopolitan itself issues raw syscalls on x86-64 XNU. The design is sound in principle, and as of 2026-09-02 the syscall surface behind it is complete — clone, futex, the netpoller and the rest all landed. **It is still unverified.** There is no Intel-mac CI runner, so nothing here has ever executed. Do not claim it works. |
| 1b | same file, `darwin_error` (FIXED 2026-09-02) | The darwin return path tested for failure with the LINUX convention (`AX > -4096`). XNU signals failure with the CARRY FLAG and returns a POSITIVE errno, so every failing syscall was reported as SUCCESS carrying the errno as its result — `ENOENT` came back as a result of 2. Now `JCS`, followed by a call to `runtime·cosmo_xlat_errno_ax` for the numbering. See Section 4.1c: the same defect ran through the runtime's own darwin branches. |
| 1c | `src/runtime/sys_cosmo_amd64.s` (FIXED 2026-09-02) | The identical carry-flag defect, in the runtime's own raw-XNU branches. `open` returned the errno as a file descriptor (`ENOENT` opened "fd 2"); `read`/`write1` returned it as a byte count, so a failed write read as a short write; `pipe2` produced a pair of fds out of it; `mmap` returned a mapping at address `ENOMEM` that every caller then wrote to. Each failing branch now tests the carry flag and, where the value reaches Go, translates the errno. `munmap` and `rtsigprocmask` keep their crash-on-failure pokes, now reached by `JCC`. |
| 2 | ~~`cosmoDarwinKqueueSupported` returns false~~ | Fixed with Section 3.5: the poller is served by raw XNU syscall. What is left on macOS-Intel is thread creation and parking, which is a narrower and more accurate claim than "the netpoller is unsupported". |
| 3 | `src/runtime/os_cosmo_nt_arm64.go` | **The Win32 layer is implemented on arm64; the boot path and the netpoller are not.** `sys_cosmo_nt_arm64.s` supplies the AAPCS64 `ntcall6`/`ntcall10` trampolines (TEB via `R18_PLATFORM`, the `SetLastError(0)` bracket), the thread start, the VEH/VCH thunks and the console/signal trampolines, and `os_cosmo_nt_ctx_arm64.go` supplies the `ARM64_NT_CONTEXT` record and the five operations preemption performs on it. Every file that was `cosmo && amd64` is now `cosmo`, so `ntFutexsleep`, `ntNewosproc`, `ntVirtualAlloc`/`Free`, `ntSigaction`, `ntGoenvs`, `ntPreemptM`, `ntMinitThread`, `ntInitConsoleCtrl` and the profiler are live code on both arches. Sourcing is upstream's own windows/arm64 port (`runtime/sys_windows_arm64.s`, `internal/runtime/syscall/windows/defs_windows_arm64.go`, `signal_windows_arm64.go`). Still throwing: `netpollinitNT` and friends, because they sit on the syscall-emulation layer, which is written against Linux **amd64** numbering — arm64 has no bare `stat`/`lstat`/`open`, its `struct stat` is 144 bytes rather than 128, and `O_DIRECTORY` differs. That split is its own job. **Still unreachable, and still enforced rather than asserted**: `iswindows` remains a constant `false` on arm64, because no APE boot stub sets `__hostos` there (`rt0_cosmo_nt_amd64.s` has no arm64 twin and the APE has no arm64 PE header), and `TestPlatformTableIsClosed` (`cmd/internal/cosmoape`, run unfiltered by the ubuntu leg) fails if a windows row for a non-amd64 arch is added to the linker's boot-mechanism table without the runtime to back it. |
| 4 | `src/internal/runtime/syscall/cosmo/asm_cosmo_arm64.s` (`darwin_sigaction`, FIXED 2026-09-02) | Found while fixing the amd64 stub in Section 2.2. The arm64 branch forwarded `rt_sigaction`'s own arguments to `syslib.sigaction`, which is Apple libc's `sigaction`: it wants an APPLE signal number, so a handler installed for Linux SIGUSR1 (10) landed on Apple SIGBUS; and it wants Apple's 16-byte LIBC `struct sigaction` {handler, mask u32, flags i32}, so given the 32-byte Linux one it read `sa_mask` out of the low half of `sa_flags` and `sa_flags` out of the high half, which is zero — SA_SIGINFO, SA_ONSTACK and SA_RESTART were all silently lost and the mask was whatever the flags word happened to be. Reading `old` back wrote 16 Apple bytes into a 32-byte Linux struct and left the caller reading garbage flags, restorer and mask. Only the error convention was right: the Syslib entry is sysret-wrapped and `darwin_call` already handles `-errno`. The branch now calls `darwinSigactionSyslib` (`sigaction_cosmo_arm64.go`), which translates both structs over the same shared tables the amd64 side uses; the assembly passes the Syslib entry's address, since this package cannot reach `runtime.__syslib` from Go. Nothing in the runtime went through here — it installs handlers via `runtime.darwinSigaction` — so this served `syscall.RawSyscall` callers only, and CI never exercised it. |

---

## 5a. NT metadata syscalls (partly CLOSED — metadata wave, 2026-09-02)

The NT emulation already served fsync, ftruncate, fchmod, chdir and getcwd. The metadata wave added the four Win32 can serve and programs actually reach (`src/runtime/os_cosmo_nt_meta.go`):

| syscall | Win32 | notes |
|---|---|---|
| `utimensat` | `SetFileTime` | Win32's own convention IS the sentinel: a NULL stamp pointer means "leave alone", which is exactly UTIME_OMIT. UTIME_NOW is filled from `GetSystemTimeAsFileTime`. |
| `truncate` | `SetEndOfFile` | The path form of the ftruncate NT already had; NT resizes only through a handle, so it opens one. |
| `fchdir` | `GetFinalPathNameByHandleW` + `SetCurrentDirectoryW` | NT has no handle-relative chdir. The `\\?\` prefix is trimmed for the drive form, because SetCurrentDirectoryW rejects it. |
| `linkat` | `CreateHardLinkW` | Argument order is reversed against linkat. |

These four are resolved from kernel32 OPTIONALLY: a zero pointer answers ENOSYS at the use site rather than poking a crash address at boot over a call most programs never make.

**Still ENOSYS on NT**, because Windows has no counterpart and upstream's own windows port does not expose them either: `statfs`/`fstatfs`, `uname`, `prlimit64`, `getpriority`/`setpriority`, `getgroups`/ `setgroups`, the uid/gid setters, `chroot`, `sendfile`, `mknodat`, and `symlinkat` (this port resolves no symlinks at all — `readlinkat` answers only `/proc/self/exe`). `fchmod`/`fchmodat` remain a documented no-op after an existence check, and `fchown`/`fchownat` have no unix ownership to change; see the reasoning in `ntEmuFchmod`.

The runtimeprobe split follows exactly this line: `fsmeta` is a hard assertion on all three hosts, `fsmetaunix` and `sysinfo` report rather than fail on a Windows host.

## 5. NT gaps (Windows host)

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/runtime/os_cosmo_nt_msg.go:119` | SCM_RIGHTS on an INET/INET6 socket is **silently DROPPED** (data still sent). |
| 2 | `src/runtime/os_cosmo_nt_msg.go:99` | `ExtraFiles` is ENOSYS — a socketpair end can never reach another process. |
| 3 | `src/runtime/os_cosmo_amd64.go:90-91` | Signal sends are still dropped on NT (comment: "Signal sends are still dropped on NT; the thread id becomes load-bearing in the signals/preemption wave"). |
| 4 | `src/runtime/os_cosmo_nt_prof.go:73` | CPU profiling stays silently sampleless in some configurations (pre-item-3 behavior). |

---

## 6. macOS arm64: the syscall gap (CLOSED — metadata wave, 2026-09-02)

The arm64 slow path (`syscall6SlowDarwin`) handles a fixed set of syscalls. Everything else returns `darwinENOSYS`. That set used to stop at the file-I/O, socket and process syscalls the apetest path reaches, so every metadata and system-information call fell through — `Fsync`, `Ftruncate`, `Truncate`, `Fchmod`, `Fchown`, `Fchdir`, `Statfs`, `Fstatfs`, `Uname`, `Getrlimit`, `Getpriority`, `Setpriority`, `Getpgid`, `Setuid`, `Setgid`, `Setreuid`, `Setregid`, `Chroot`, `Sendfile`, `getgroups`, `setgroups`, `fchmodat`, `fchownat`, `linkat`, `symlinkat`, `utimensat` (and with it `utimes`, `futimesat` and `os.Chtimes`), `Mknodat`, and `prlimit`.

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

**Still ENOSYS on macOS, because Apple has no counterpart:** `Setresuid`, `Setresgid`, `Setfsuid`, `Setfsgid`, and `mknodat` with a directory descriptor. `Fchmodat` reports `EOPNOTSUPP` for `AT_SYMLINK_NOFOLLOW` on every host: the Linux syscall takes no flags, and one APE answering the same call differently per host is worse than answering it consistently.

**Coverage.** The pure translation tables are unit-tested where cosmo tests run (`darwinabi_cosmo_test.go`, `bigbuf_cosmo_test.go`, both on the ubuntu CI leg). The syscalls themselves are exercised end to end by `testdata/runtimeprobe`'s `fsmeta`, `sysinfo` and `sendfile` checks, which the apetest suite runs on all three CI runners — the macOS runner is what proves the dlsym wiring. Those checks report rather than fail on a Windows host, following `checkDupFile`: the NT emulation has not brought this surface up, and the log records what it answered.

---

## 6b. macOS-Intel: the syscall table (CLOSED — metadata wave, 2026-09-02)

The amd64 darwin dispatch carried 18 syscalls and answered everything else ENOSYS. It now also serves fsync, truncate, ftruncate, fchmod, fchown, fchdir, chroot, get/setgroups, get/setpriority, setuid, setgid, setreuid, setregid, getpgid, and statfs/fstatfs (the last two through XNU's statfs64/fstatfs64, with the same buffer-size guard the arm64 path applies, so a Linux-layout buffer is refused rather than overrun). `getpriority` applies the Linux `20-nice` bias, which the carry-flag convention makes unambiguous — the value alone cannot say whether the call failed.

Failures now report LINUX errno numbers. `runtime·cosmo_xlat_errno_ax` (`sys_cosmo_amd64.s`) is the amd64 twin of arm64's `cosmo_xlat_errno_r0`, over the one shared table in `sys_cosmo_errno.s`, and a linkname push makes it reachable from this package's assembly — the same mechanism arm64 already used. An earlier note here claimed that could not be done. It was reasoning about the DATA table rather than the function that wraps it.

**Every BSD number came from `syscall/zsysnum_darwin_amd64.go`**, the tree's own authority, not from memory. That mattered: statfs64 is 345, not the 338 recall suggested. Anything that file does not carry is deliberately absent rather than guessed — a wrong syscall number does not fail, it calls a DIFFERENT syscall. That is why the *at family (`linkat`, `symlinkat`, `fchmodat`, `fchownat`) and `utimensat` stay ENOSYS on amd64 while arm64 serves them: arm64 resolves by NAME through dlsym and never needs a number. `uname` stays ENOSYS for a different reason — XNU has no uname syscall at all. It is a libc function over sysctl. `sendfile` stays ENOSYS because Apple's argument order, its value-result count pointer and its untouched file offset need real control flow, which the arm64 Go slow path has and an assembly dispatch does not.

This closes the syscall TABLE. It does not bring up macOS-Intel: see Section 4.1.

## 7. What this means

- The macOS arm64 syscall surface is complete for everything the `syscall` package exposes and Apple can serve (Section 6). What remains there is the handful Apple genuinely lacks.
- The "return success" stubs (Section 2) are still the highest-risk items: they hide failures.
- The macOS-Intel (amd64) SYSCALL surface is closed: the table, the error convention, the errno numbering, the netpoller, the CPU count, parking, thread creation, TLS and nanosleep. So are signals in the runtime: `darwinSigaction` issues a real `__sigaction` with its own trampoline (Section 2.3), and `darwinSigprocmask` bridges the sigset width (Section 2.6). The `syscall` package's own `rt_sigaction` emulation installs handlers for real too (Section 2.2). None of it is verified, because there is no Intel-mac runner — read and reasoned about, never executed.
- Windows/arm64 (Section 4.3) has its Win32 layer now — trampolines, thread start, exception dispatch, preemption context. It is still unreachable (no APE boot path, no platform token), and its netpoller still throws, waiting on an arm64 split of the syscall emulation.

## 8. Recommended follow-up

1. ~~Implement the remaining darwin arm64 syscalls in `syscall6SlowDarwin`.~~ Done; see Section 6.
2. ~~Replace the remaining amd64 darwin "return success" stub, `darwin_sigaction` in `internal/runtime/syscall/cosmo`.~~ Done; see Section 2.2.
3. **The macOS-Intel syscall surface is closed.** Every item once filed under this heading is done: the metadata table (Section 6b), the error convention and errno numbering (Section 4.1b/4.1c), the netpoller (Section 3.5), the CPU count (Section 3.6), parking (Section 3.1), thread creation and TLS (Section 3.2/3.2b), and nanosleep (Section 2.1). Signals in the runtime are done as well: a real `__sigaction` with a translated struct and its own trampoline (Section 2.3), and a mask path that remaps the sigset instead of handing a kernel that reads 4 bytes the Linux 8 (Section 2.6). The `syscall` package's `rt_sigaction` emulation is done too (item 2).

   None of it is verified. There is no Intel-mac runner, so every line of the above has been read and reasoned about but never executed. That remains the honest blocker on the platform, and `Default()` leaves darwin/amd64 out of a default build for exactly this reason.
4. Windows/arm64: the `ntcall` trampolines, the `CONTEXT` layout and the VEH thunks are written (Section 4.3), sourced from upstream's own windows/arm64 port. Two things are left. The netpoller needs the syscall emulation split per architecture — numbers, `struct stat` width and `O_DIRECTORY` all differ. And the boot mechanism does not exist: the linker has no way to emit an arm64 PE header, so nothing sets `__hostos` there. Until it does, none of this code can be started, let alone tested, and `iswindows` stays a constant `false` so the compiler deletes it.
5. Give `sendfile` an in-tree consumer. `internal/poll/sendfile_unix.go` carries no cosmo build tag, so `io.Copy` from a file to a socket never reaches the syscall on any cosmo host. Only a caller that uses `syscall.Sendfile` directly does. Adding cosmo to that tag needs the NT emulation to serve sendfile too, which it does not yet.
