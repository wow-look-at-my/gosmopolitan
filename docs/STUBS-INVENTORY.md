# Cosmo syscall-emulation stub inventory

This document catalogs stubbed-out or broken functionality in the cosmo
syscall emulation and runtime, beyond the `os.File.Seek`/`os.File.ReadAt`
gap that this branch fixes. Each item lists the file, line, and the
observed behavior. The goal is a complete, honest accounting of what
"works" only by pretending to, what fails loudly, and what is
structurally broken — so follow-up work can be triaged.

The single most important finding: **the darwin (macOS) syscall
emulation is incomplete, and the amd64 darwin path is largely stubbed
and structurally broken.** `Seek`/`ReadAt` were one instance of a
broader pattern, not an isolated bug.

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
| 7 | `src/syscall/syscall_cosmo_arm64.go:281-284` (`Getpgrp`) | Calls `Getpgid(0)` and **discards the error**, returning 0. On macOS `Getpgid` is ENOSYS, so `Getpgrp` silently returns 0 (wrong value) with no error. |

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
| 1 | `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s` (whole darwin path) | The amd64 darwin path issues **raw XNU `SYSCALL` instructions** (lines 103, 243, 285). Per CLAUDE.md, macOS does not allow raw syscalls — "Code that uses raw `SYSCALL`/`SVC` instructions will crash with SIGSYS on macOS." So the entire amd64 darwin path is structurally broken, not just the missing syscalls. macOS-Intel runtime bring-up is untested per CLAUDE.md. |
| 2 | `src/runtime/os_cosmo_amd64.go:67-69` | `cosmoDarwinKqueueSupported` returns false: "macOS-Intel execution is not implemented: clone/futex are ENOSYS there". The netpoller is unsupported on amd64. |
| 3 | `src/runtime/os_cosmo_nt_arm64.go:24-121` | **Windows/arm64 is entirely not implemented.** Every `nt*` function (`ntFutexsleep`, `ntNewosproc`, `ntVirtualAlloc`, `ntSigaction`, `ntGoenvs`, `ntPreemptM`, `ntMinitThread`, `ntInitConsoleCtrl`, `ntSetProcessCPUProfiler`, `ntVirtualFree`, and all `netpoll*NT`) throws "not implemented on arm64". |

---

## 5. NT gaps (Windows host)

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/runtime/os_cosmo_nt_msg.go:119` | SCM_RIGHTS on an INET/INET6 socket is **silently DROPPED** (data still sent). |
| 2 | `src/runtime/os_cosmo_nt_msg.go:99` | `ExtraFiles` is ENOSYS — a socketpair end can never reach another process. |
| 3 | `src/runtime/os_cosmo_amd64.go:90-91` | Signal sends are still dropped on NT (comment: "Signal sends are still dropped on NT; the thread id becomes load-bearing in the signals/preemption wave"). |
| 4 | `src/runtime/os_cosmo_nt_prof.go:73` | CPU profiling stays silently sampleless in some configurations (pre-item-3 behavior). |

---

## 6. macOS arm64: syscalls that fall through to ENOSYS (empirically verified)

The arm64 slow path (`syscall6SlowDarwin`) handles a fixed set of
syscalls; everything else returns `darwinENOSYS`. The following
`syscall`-package functions return `ENOSYS` ("function not implemented")
on macOS arm64, verified by running a probe against the shipped
toolchain on this host (output captured in the goal scratch dir):

```
Fsync          err=function not implemented
Ftruncate      err=function not implemented
Fchmod         err=function not implemented
Fchown         err=function not implemented
Fchdir         err=function not implemented
Fstatfs        err=function not implemented
Statfs         err=function not implemented
Truncate       err=function not implemented
Getrlimit      err=function not implemented
Getpriority    err=function not implemented
Setpriority    err=function not implemented
Getpgid        err=function not implemented
Setuid         err=function not implemented
Setgid         err=function not implemented
Setreuid       err=function not implemented
Setregid       err=function not implemented
Chroot         err=function not implemented
Sendfile       err=function not implemented
```

Additional syscalls declared in `src/syscall/syscall_cosmo.go` that are
not in the arm64 slow-path switch and therefore also fall through to
ENOSYS (not all are exposed as callable `syscall` functions, but the
underlying syscall is unemulated): `fchmodat`, `linkat`, `symlinkat`,
`utimensat`, `Mknodat`, `Fchownat`, `getgroups`, `setgroups`, `utimes`
(returns EINVAL, not ENOSYS — partially handled), `futimesat`,
`Setfsuid`, `Setfsgid`, `Setresuid`, `Setresgid`, `Uname`, `getrlimit`,
`prlimit`.

**Impact:** on macOS arm64, `os.File.Sync()`, `os.File.Truncate()`,
`os.File.Chmod()`, `os.File.Chown()`, `os.Statfs`, `syscall.Getrlimit`,
and the uid/gid setters all fail with ENOSYS. The standard library's
`os` package does not call these on the apetest path, so CI stays green
— but any program that uses them fails on macOS.

---

## 7. What this means

- The `Seek`/`ReadAt` fix on this branch is real and verified, but it is
  **one of many** darwin-emulation gaps. The macOS arm64 surface is
  missing ~20+ syscalls, and the macOS-Intel (amd64) surface is largely
  stubbed or structurally broken.
- The "return success" stubs (Section 2) are the highest-risk items:
  they hide failures. `time.Sleep` and signal handling on macOS-Intel
  silently do nothing.
- Windows/arm64 (Section 4.3) is entirely unimplemented.

## 8. Recommended follow-up (out of scope for this branch)

1. Implement the remaining darwin arm64 syscalls in
   `syscall6SlowDarwin` (Fsync, Ftruncate, Fchmod, Fchown, Fchdir,
   Fstatfs/Statfs, Truncate, Getrlimit, Getpriority/Setpriority,
   Getpgid, Setuid/Setgid/Setreuid/Setregid, Chroot, Sendfile) — all
   are fixed-arity libc calls with identical layouts, so they follow the
   same `darwinCall` pattern as the Seek/ReadAt fix.
2. Replace the amd64 darwin "return success" stubs (`darwin_nanosleep`,
   `darwin_sigaction`, `rt_sigaction`) with real Syslib/dlsym-backed
   implementations, or make them fail loudly instead of silently.
3. Decide the fate of the amd64 darwin raw-XNU path: either bring up
   macOS-Intel properly (Syslib-based, like arm64) or refuse to run
   rather than crash with SIGSYS.
4. Implement Windows/arm64, or refuse to boot on it.
