# Cosmo syscall-emulation stub inventory

This document catalogs stubbed-out or broken functionality in the cosmo
syscall emulation and runtime. Each item lists the file, line, and the
observed behavior. The goal is a complete, honest accounting of what
"works" only by pretending to, what fails loudly, and what is
structurally broken — so follow-up work can be triaged.

The finding this document was opened on: **the darwin (macOS) syscall
emulation is incomplete, and the amd64 darwin path is largely stubbed
and structurally broken.** `Seek`/`ReadAt` were one instance of a
broader pattern, not an isolated bug.

The arm64 half of that is now closed — Section 6 lists what the metadata
wave implemented and the few calls Apple genuinely cannot serve. The
amd64 half stands as written. The same wave closed the NT metadata
syscalls Win32 can serve (Section 5a).

The same wave also closed the macOS-Intel SYSCALL TABLE (Section 6b) and
fixed the error-detection bug underneath it (Section 4.1b).

**Scope note.** "Missing syscall" here means a call the host CAN serve
that the emulation did not. What remains after this wave is not that:

- **macOS-Intel** has its syscall table now, but no runtime bring-up —
  clone, futex and the netpoller are still ENOSYS (Section 3), and
  nothing there is verified because there is no Intel-mac CI runner.
- **Windows/arm64** (Section 4.3) is a port that does not exist: every
  `nt*` function throws. Also no runner.

Both are Section 8 items 2-4. Neither is a syscall table, and neither
can be written and *proven* the way this wave was.

---

## 1. The Seek/ReadAt gap (FIXED on this branch)

**Before this branch:** on macOS (both arm64 and amd64), `os.File.Seek`
and `os.File.ReadAt` returned `ENOSYS` ("function not implemented").
Linux and Windows already dispatched `pread`/`lseek` natively, so the
gap was macOS-only — but it was real and user-visible.

- arm64: `SYS_PREAD64`/`SYS_PWRITE64`/`SYS_LSEEK` were absent from the
  `syscall6SlowDarwin` switch in
  `src/internal/runtime/syscall/cosmo/syscall_cosmo_arm64.go`, so they
  fell through to the `darwinENOSYS` default.
- amd64: `SYS_PREAD64`/`SYS_PWRITE64`/`SYS_LSEEK` were absent from the
  darwin dispatch in `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s`,
  so they fell through to `darwin_enosys`.

**Fix (this branch):** added `Pread`/`Pwrite`/`Lseek` to `DarwinFns`
(dlsym-resolved via the Syslib), handled the three syscalls in the arm64
slow path, and added the three XNU dispatch entries to the amd64 asm.
Verified on macOS arm64: all three `Seek` whence values and `ReadAt`
(mid-file, past-EOF, at-EOF) return correct results, 5/5 runs. A
regression check (`seekreadat`) is wired into `testdata/runtimeprobe`,
which the CI matrix runs on Linux, macOS, and Windows.

---

## 2. Silent "return success" stubs (most dangerous)

These pretend to succeed while doing nothing. A caller cannot tell the
operation was skipped. These are the worst kind of stub.

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s:224-228` (`darwin_nanosleep`) | Returns success (r1=0, errno=0) **without sleeping**. Comment: "For now, just return success (sleep is best-effort)". `time.Sleep` on macOS-Intel silently does nothing. |
| 2 | `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s:255-258` (`darwin_sigaction`) | Returns success **without installing the signal handler**. Comment: "For basic functionality, return success". Signal delivery on macOS-Intel is silently broken. |
| 3 | `src/runtime/sys_cosmo_amd64.s:618-620` (`rt_sigaction` darwin branch) | Returns success without calling sigaction. Comment: "For now, return success without calling sigaction / TODO: Implement proper sigaction translation layer". |
| 4 | `src/runtime/sys_cosmo_amd64.s:623-625` (`rt_sigaction` NT branch) | Returns success on NT wave 1 (no signal machinery). Comment: "the same benign lie the darwin stub above tells". |
| 5 | `src/runtime/sys_cosmo_amd64.s:601` (`rtsigprocmask_nt`) | Returns success on NT (no signal machinery). |
| 6 | `src/runtime/sys_cosmo_amd64.s:566-568` (`rtsigprocmask` darwin) | Translates `how` but the 8-byte Linux sigset vs 4-byte Apple sigset mismatch remains — comment: "macOS signal handling is still stubbed and tracked for the signal-translation wave". |
| 7 | ~~`src/syscall/syscall_cosmo_arm64.go` (`Getpgrp`)~~ | Was: calls `Getpgid(0)` and discards the error, so an ENOSYS `Getpgid` on macOS silently returned 0. `Getpgid` is emulated now (Section 6), and `getpgid(0)` cannot fail; the discard matches what the linux port does. The runtimeprobe `sysinfo` check asserts `Getpgrp() == Getpgid(0)` and that both are positive, so a regression to the silent zero fails CI. |

---

## 3. ENOSYS stubs (visible failure, functionality missing)

These fail loudly with `ENOSYS`, so they are honest — but the
functionality is missing.

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/runtime/sys_cosmo_amd64.s:843-844` (`futex` darwin) | Returns ENOSYS. macOS has no futex. |
| 2 | `src/runtime/sys_cosmo_amd64.s:919-921` (`clone` darwin) | Returns ENOSYS. Comment: "Thread creation on macOS should use pthread_create". |
| 3 | `src/runtime/sys_cosmo_arm64.s:1099-1101` (`futex` darwin) | Returns ENOSYS. Comment: "use dispatch_semaphore ... For now, return ENOSYS". |
| 4 | `src/runtime/sys_cosmo_arm64.s:654-655` (`mincore` darwin) | Returns -1 (ENOSYS). "mincore not in Syslib". |
| 5 | `src/runtime/os_cosmo_amd64.go:75-80` (`cosmoDarwinKqueue`/`cosmoDarwinKevent`) | Return ENOSYS. The darwin netpoller is unsupported on amd64. |
| 6 | `src/runtime/os_cosmo_amd64.go:63` (`cosmoDarwinNumCPU`) | Returns 0 ("unknown"), so `getCPUCount` falls back to 1 on macOS-Intel. |
| 7 | `src/runtime/os_cosmo_amd64.go:127` (`darwinSigprocmask`) | `throw("darwinSigprocmask: not implemented on amd64")`. |
| 8 | `src/runtime/os_cosmo_amd64.go:131` (`darwinSigaction`) | Returns -1 (unreachable on amd64; stub for linking). |

---

## 4. Structurally broken / untested paths

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s` (whole darwin path) | The amd64 darwin path issues **raw XNU `SYSCALL` instructions**. CLAUDE.md's blanket "macOS does not allow raw syscalls" is overbroad: that restriction is what ARM64 macOS enforces, which is why arm64 needs the Syslib and dlsym at all, and cosmopolitan itself issues raw syscalls on x86-64 XNU. The amd64 design is therefore sound in principle — but macOS-Intel runtime bring-up (clone/futex/netpoller) is still absent, and there is no Intel-mac CI runner, so none of it is verified. Do not claim it works. |
| 1b | same file, `darwin_error` (FIXED 2026-09-02, detection half) | The darwin return path tested for failure with the LINUX convention (`AX > -4096`). XNU signals failure with the CARRY FLAG and returns a POSITIVE errno, so every failing syscall was reported as SUCCESS carrying the errno as its result — `ENOENT` came back as a result of 2. Now `JCS`. The errno NUMBERING is still Apple's: the translation table is a DATA symbol in package runtime, and a data symbol cannot be pushed across packages the way `cosmo_xlat_errno_r0` is, so closing it needs a runtime-side entry point the amd64 dispatch can call. |
| 2 | `src/runtime/os_cosmo_amd64.go:67-69` | `cosmoDarwinKqueueSupported` returns false: "macOS-Intel execution is not implemented: clone/futex are ENOSYS there". The netpoller is unsupported on amd64. |
| 3 | `src/runtime/os_cosmo_nt_arm64.go:24-121` | **Windows/arm64 is entirely not implemented.** Every `nt*` function (`ntFutexsleep`, `ntNewosproc`, `ntVirtualAlloc`, `ntSigaction`, `ntGoenvs`, `ntPreemptM`, `ntMinitThread`, `ntInitConsoleCtrl`, `ntSetProcessCPUProfiler`, `ntVirtualFree`, and all `netpoll*NT`) throws "not implemented on arm64". |

---

## 5a. NT metadata syscalls (partly CLOSED — metadata wave, 2026-09-02)

The NT emulation already served fsync, ftruncate, fchmod, chdir and
getcwd. The metadata wave added the four Win32 can serve and programs
actually reach (`src/runtime/os_cosmo_nt_meta.go`):

| syscall | Win32 | notes |
|---|---|---|
| `utimensat` | `SetFileTime` | Win32's own convention IS the sentinel: a NULL stamp pointer means "leave alone", which is exactly UTIME_OMIT. UTIME_NOW is filled from `GetSystemTimeAsFileTime`. |
| `truncate` | `SetEndOfFile` | The path form of the ftruncate NT already had; NT resizes only through a handle, so it opens one. |
| `fchdir` | `GetFinalPathNameByHandleW` + `SetCurrentDirectoryW` | NT has no handle-relative chdir. The `\\?\` prefix is trimmed for the drive form, because SetCurrentDirectoryW rejects it. |
| `linkat` | `CreateHardLinkW` | Argument order is reversed against linkat. |

These four are resolved from kernel32 OPTIONALLY: a zero pointer answers
ENOSYS at the use site rather than poking a crash address at boot over a
call most programs never make.

**Still ENOSYS on NT**, because Windows has no counterpart and upstream's
own windows port does not expose them either: `statfs`/`fstatfs`,
`uname`, `prlimit64`, `getpriority`/`setpriority`, `getgroups`/
`setgroups`, the uid/gid setters, `chroot`, `sendfile`, `mknodat`, and
`symlinkat` (this port resolves no symlinks at all — `readlinkat`
answers only `/proc/self/exe`). `fchmod`/`fchmodat` remain a documented
no-op after an existence check, and `fchown`/`fchownat` have no unix
ownership to change; see the reasoning in `ntEmuFchmod`.

The runtimeprobe split follows exactly this line: `fsmeta` is a hard
assertion on all three hosts, `fsmetaunix` and `sysinfo` report rather
than fail on a Windows host.

## 5. NT gaps (Windows host)

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/runtime/os_cosmo_nt_msg.go:119` | SCM_RIGHTS on an INET/INET6 socket is **silently DROPPED** (data still sent). |
| 2 | `src/runtime/os_cosmo_nt_msg.go:99` | `ExtraFiles` is ENOSYS — a socketpair end can never reach another process. |
| 3 | `src/runtime/os_cosmo_amd64.go:90-91` | Signal sends are still dropped on NT (comment: "Signal sends are still dropped on NT; the thread id becomes load-bearing in the signals/preemption wave"). |
| 4 | `src/runtime/os_cosmo_nt_prof.go:73` | CPU profiling stays silently sampleless in some configurations (pre-item-3 behavior). |

---

## 6. macOS arm64: the syscall gap (CLOSED — metadata wave, 2026-09-02)

The arm64 slow path (`syscall6SlowDarwin`) handles a fixed set of
syscalls; everything else returns `darwinENOSYS`. That set used to stop
at the file-I/O, socket and process syscalls the apetest path reaches,
so every metadata and system-information call fell through — `Fsync`,
`Ftruncate`, `Truncate`, `Fchmod`, `Fchown`, `Fchdir`, `Statfs`,
`Fstatfs`, `Uname`, `Getrlimit`, `Getpriority`, `Setpriority`,
`Getpgid`, `Setuid`, `Setgid`, `Setreuid`, `Setregid`, `Chroot`,
`Sendfile`, `getgroups`, `setgroups`, `fchmodat`, `fchownat`, `linkat`,
`symlinkat`, `utimensat` (and with it `utimes`, `futimesat` and
`os.Chtimes`), `Mknodat`, and `prlimit`.

All of them are emulated now, in
`internal/runtime/syscall/cosmo/{file,proc,darwinabi}_cosmo*.go`. Most
are fixed-arity Apple libc entries whose arguments and constants already
agree, so they pass straight through `darwinCall`. The ones that do not:

| syscall | why it needed more than a forward |
|---|---|
| `getpriority` | Apple returns the nice value; the Linux syscall returns `20-nice`. -1 is a legal result, so errno is cleared first and read back. |
| `prlimit64` | Apple's getrlimit/setrlimit take no pid, number the resources differently above `RLIMIT_CORE`, and use a different infinity sentinel. |
| `utimensat` | `UTIME_NOW`/`UTIME_OMIT` are `(1<<30)-1`/`-2` on Linux and `-1`/`-2` on Apple. |
| `sendfile` | Apple takes the file first and the socket second, reports the count through a pointer (filled even on failure), and never moves the file offset. |
| `linkat`, `fchownat` | `AT_SYMLINK_FOLLOW`/`AT_SYMLINK_NOFOLLOW` have different values. |
| `mknodat` | Apple has no directory-relative form. Served for `AT_FDCWD` (what `Mknod`/`Mkfifo` pass) over `mknod`; any other dirfd is ENOSYS rather than a path silently resolved against the wrong directory. |
| `statfs`, `fstatfs`, `uname` | Apple's structs are 2168 and 1280 bytes, far past the nosplit budget the dispatch spine runs under. The Apple-layout buffer is allocated in package `syscall` (`bigbuf_cosmo*.go`) and converted there; the emulation refuses a buffer smaller than the Apple struct, so a caller that reached `SYS_STATFS` with a Linux `Statfs_t` gets EINVAL instead of a two-kilobyte overrun. |

Two Linux `Statfs_t` fields have no Apple source. `Type` carries Apple's
own filesystem-type number (the choice `Stat_t.Dev` already makes for
device numbers) and `Namelen` stays zero rather than carrying a guess.
`Utsname.Domainname` stays empty for the same reason.

**Still ENOSYS on macOS, because Apple has no counterpart:**
`Setresuid`, `Setresgid`, `Setfsuid`, `Setfsgid`, and `mknodat` with a
directory descriptor. `Fchmodat` reports `EOPNOTSUPP` for
`AT_SYMLINK_NOFOLLOW` on every host: the Linux syscall takes no flags,
and one APE answering the same call differently per host is worse than
answering it consistently.

**Coverage.** The pure translation tables are unit-tested where cosmo
tests run (`darwinabi_cosmo_test.go`, `bigbuf_cosmo_test.go`, both on
the ubuntu CI leg). The syscalls themselves are exercised end to end by
`testdata/runtimeprobe`'s `fsmeta`, `sysinfo` and `sendfile` checks,
which the apetest suite runs on all three CI runners — the macOS runner
is what proves the dlsym wiring. Those checks report rather than fail on
a Windows host, following `checkDupFile`: the NT emulation has not
brought this surface up, and the log records what it answered.

---

## 6b. macOS-Intel: the syscall table (CLOSED — metadata wave, 2026-09-02)

The amd64 darwin dispatch carried 18 syscalls and answered everything
else ENOSYS. It now also serves fsync, truncate, ftruncate, fchmod,
fchown, fchdir, chroot, get/setgroups, get/setpriority, setuid, setgid,
setreuid, setregid, getpgid, and statfs/fstatfs (the last two through
XNU's statfs64/fstatfs64, with the same buffer-size guard the arm64 path
applies, so a Linux-layout buffer is refused rather than overrun).
`getpriority` applies the Linux `20-nice` bias, which the carry-flag
convention makes unambiguous — the value alone cannot say whether the
call failed.

**Every BSD number came from `syscall/zsysnum_darwin_amd64.go`**, the
tree's own authority, not from memory. That mattered: statfs64 is 345,
not the 338 recall suggested. Anything that file does not carry is
deliberately absent rather than guessed — a wrong syscall number does
not fail, it calls a DIFFERENT syscall. That is why the *at family
(`linkat`, `symlinkat`, `fchmodat`, `fchownat`) and `utimensat` stay
ENOSYS on amd64 while arm64 serves them: arm64 resolves by NAME through
dlsym and never needs a number. `uname` stays ENOSYS for a different
reason — XNU has no uname syscall at all; it is a libc function over
sysctl. `sendfile` stays ENOSYS because Apple's argument order, its
value-result count pointer and its untouched file offset need real
control flow, which the arm64 Go slow path has and an assembly dispatch
does not.

This closes the syscall TABLE. It does not bring up macOS-Intel: see
Section 4.1.

## 7. What this means

- The macOS arm64 syscall surface is complete for everything the
  `syscall` package exposes and Apple can serve (Section 6). What
  remains there is the handful Apple genuinely lacks.
- The "return success" stubs (Section 2) are now the highest-risk items:
  they hide failures. `time.Sleep` and signal handling on macOS-Intel
  silently do nothing.
- The macOS-Intel (amd64) surface is still largely stubbed or
  structurally broken, and Windows/arm64 (Section 4.3) is entirely
  unimplemented.

## 8. Recommended follow-up

1. ~~Implement the remaining darwin arm64 syscalls in
   `syscall6SlowDarwin`.~~ Done; see Section 6.
2. Replace the amd64 darwin "return success" stubs (`darwin_nanosleep`,
   `darwin_sigaction`, `rt_sigaction`) with real implementations, or make
   them fail loudly instead of silently. These are now the largest
   remaining lie in the tree.
3. Bring up macOS-Intel: clone/futex/netpoller (Section 3), and give the
   amd64 dispatch a runtime-side entry point so it can translate Apple
   errnos (Section 4.1b). The syscall table itself is done (Section 6b).
   None of this can be verified until an Intel-mac runner exists, which
   is the honest blocker on the whole platform.
4. Implement Windows/arm64, or refuse to boot on it.
5. Give `sendfile` an in-tree consumer. `internal/poll/sendfile_unix.go`
   carries no cosmo build tag, so `io.Copy` from a file to a socket
   never reaches the syscall on any cosmo host; only a caller that uses
   `syscall.Sendfile` directly does. Adding cosmo to that tag needs the
   NT emulation to serve sendfile too, which it does not yet.
