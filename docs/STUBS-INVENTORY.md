# Cosmo syscall-emulation stub inventory

What is still stubbed, lying, or unverified in the cosmo syscall emulation and runtime. A row leaves this file when the work is done and a green run proves it. Finished work does not belong here.

## 1. Silent "return success" stubs

These pretend to succeed while doing nothing, so a caller cannot tell the operation was skipped.

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/runtime/sys_cosmo_amd64.s` (`rt_sigaction` NT branch) | Answered success on NT with no signal machinery behind it. Fixed on this branch, unconfirmed: `sysSigaction` routes NT to `ntSigaction`, which records the handler, so the asm branch is unreachable and crashes rather than lies. |
| 2 | `src/runtime/sys_cosmo_amd64.s` (`rtsigprocmask_nt`) | Answered success on NT while blocking nothing, so a critical section that had masked every signal could still be reentered by one. Fixed on this branch, unconfirmed: the runtime keeps its own mask (`ntSigMask`), a blocked signal waits pending, and an unblock delivers what waited. |
| 3 | `src/runtime/sys_cosmo_arm64.s` (`madvise` darwin) | Answered 0 without advising anything, so `MADV_DONTNEED` and `MADV_FREE` never reached the kernel and the heap kept every page it had touched. Fixed on this branch, unconfirmed: `osArchInit` resolves Apple's madvise through dlsym, and the advice number is translated - the two systems agree only up to 4. |

## 2. ENOSYS stubs

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/runtime/sys_cosmo_arm64.s` (`mincore` darwin) | Answered -1 always, so the page-size probe in `sysauxv` read every size as unsupported and `physPageSize` fell back to 256K. Fixed on this branch, unconfirmed: `osArchInit` resolves Apple's mincore through dlsym. |
| 2 | `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s` (`*at` family) | `linkat`, `symlinkat`, `fchmodat`, `fchownat` and `utimensat` answer ENOSYS on macOS-Intel. The BSD numbers are in `syscall/zsysnum_darwin_amd64.go`, but each needs its `AT_*` flags translated - Linux `AT_SYMLINK_NOFOLLOW` is 0x100 against Apple's 0x20 - and the amd64 dispatch is assembly with no room for a table. |
| 3 | `internal/poll/sendfile_unix.go` | Carried no cosmo build tag, so `io.Copy` from a file to a socket never reached the syscall on any cosmo host. Fixed on this branch, unconfirmed: `net` and `internal/poll` carry the tag, `ntEmuSendfile` serves NT, and the runtimeprobe `sendfile` check is now hard on Windows. |

## 3. Unverified paths

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/runtime/os_cosmo_nt_arm64.go` | **The Win32 layer is implemented on arm64; the boot path and the netpoller are not.** `sys_cosmo_nt_arm64.s` supplies the AAPCS64 `ntcall6`/`ntcall10` trampolines (TEB via `R18_PLATFORM`, the `SetLastError(0)` bracket), the thread start, the VEH/VCH thunks and the console/signal trampolines, and `os_cosmo_nt_ctx_arm64.go` supplies the `ARM64_NT_CONTEXT` record and the five operations preemption performs on it. Every file that was `cosmo && amd64` is now `cosmo`, so `ntFutexsleep`, `ntNewosproc`, `ntVirtualAlloc`/`Free`, `ntSigaction`, `ntGoenvs`, `ntPreemptM`, `ntMinitThread`, `ntInitConsoleCtrl` and the profiler are live code on both arches. Sourcing is upstream's own windows/arm64 port (`runtime/sys_windows_arm64.s`, `internal/runtime/syscall/windows/defs_windows_arm64.go`, `signal_windows_arm64.go`). Still throwing: `netpollinitNT` and friends, because they sit on the syscall-emulation layer, which is written against Linux **amd64** numbering — arm64 has no bare `stat`/`lstat`/`open`, its `struct stat` is 144 bytes rather than 128, and `O_DIRECTORY` differs. That split is its own job. **Still unreachable, and still enforced rather than asserted**: `iswindows` remains a constant `false` on arm64, because no APE boot stub sets `__hostos` there (`rt0_cosmo_nt_amd64.s` has no arm64 twin and the APE has no arm64 PE header), and `TestPlatformTableIsClosed` (`cmd/internal/cosmoape`, run unfiltered by the ubuntu leg) fails if a windows row for a non-amd64 arch is added to the linker's boot-mechanism table without the runtime to back it. |
| 2 | termios round trip | The conversion had never met a driver: the tables are unit-tested arithmetic, and the termios probe only proves the request reaches the kernel. Fixed on this branch, unconfirmed: the `pty` check opens `/dev/ptmx`, sets raw mode and reads it back, so it needs no controlling terminal. |

## 4. The distribution suite under the cosmo port

`run.bash` runs `dist test` for the cosmo port on every CI leg. These are red.

| # | Package | Behavior |
|---|---------|----------|
| 0 | every package, on the windows leg | NO cosmo binary built from this tree runs on that host, fizzbuzz included: fat and thin alike exit 2 and write nothing, with no arguments, with `-test.list=.`, and with a test named. Exit 2 with no output is a runtime throw whose print went nowhere, which puts the failure before the NT layer caches its std handles. A REGRESSION, not a gap: the published v372 toolchain (2026-08-30) builds a fizzbuzz that runs there and prints, exit 0. The PE header is not what differs - a fizzbuzz built with v372 and one built here carry the same three sections, alignments, image base and characteristics - so the break is in the runtime's NT boot. The CI probe bisects the published master releases between v372 and now. |
| 1 | `cmd/link`, `cmd/nm`, `cmd/objdump`, `cmd/pprof`, `cmd/cover`, `cmd/vet`, `cmd/compile`, `cmd/go` and `net/http/cgi`, on a macOS host | Fixed on this branch, unconfirmed: each failed on one fork/exec of an APE with "exec format error". The toolchain is built for the host, so `bin/go` there is a native darwin binary and never reaches the cosmo syscall package; the darwin child now retries through /bin/sh on ENOEXEC, as execvp(3) does. |
| 2 | `runtime`, on the ubuntu leg | Fixed on this branch, unconfirmed: `TestGoroutineProfile` took the process-wide barrier while `TestSpuriousWakeupsNeverHangSemasleep` waited on a subprocess, so both sat for the full ten minutes and the package timed out. It runs in a child process now. |
| 3 | `os` and `os/signal`, on a macOS host | Still red: `TestReadDirFD` (Apple's getdirentries reports DT_UNKNOWN for /dev/fd, `os.readdir` then stats each entry, and statting the directory's own fd answers EBADF - upstream darwin reads a directory through fdopendir for exactly this, and that port is its own job), `TestExecutableDeleted`, `TestProgWideChdir`, `TestAllThreadsSyscallSignals`. Fixed on this branch, unconfirmed: `TestHostname` reads `kern.hostname` rather than a Linux-only `/proc` path, `TestMkdirStickyUmask` chmods the sticky bit on where mkdir drops it, and the three `Root` chmod tests reach Apple's fchmodat with the flag translated. |
| 4 | `runtime` and `internal/trace`, on a macOS host | Still red: `TestCrashDumpsAllThreads` dumps one thread of four, `TestCrashHandler`, `TestDebugLogInterleaving`, `TestDebugLogSym` (the debug log prints raw values where it should symbolize), and `TestTraceStressStartStop`, which dies on `darwinSigaltstack: sigaltstack failed` - Apple refuses a new stack carrying any flag but SS_DISABLE, one under MINSIGSTKSZ, and any change made while running on it, and the throw now prints which. Fixed on this branch, unconfirmed: `TestTraceStacks` compared against the darwin PORT's libc frames on a cosmo binary. |
| 5 | four packages fixed together, on a macOS host | Unconfirmed: `crypto/x509` accepted every certificate (see the security note below), `log/syslog`'s three failures were all `os.Hostname`, `mime` measured the runner's own database, `testing/iotest` shared the log package's globals, and `crypto/internal/fips140test`'s entropy source is timing jitter that Apple's microsecond `clock_gettime` flattened. |

