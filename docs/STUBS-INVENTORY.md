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
| 1 | `src/internal/runtime/syscall/cosmo/asm_cosmo_amd64.s` (whole darwin path) | The amd64 darwin path issues raw XNU `SYSCALL` instructions, which is legal on x86-64 XNU - the no-raw-syscalls rule is an ARM64 restriction. The syscall surface behind it is complete. **Nothing here has ever executed**: there is no Intel-mac CI runner. Do not claim it works. |
| 2 | `src/runtime/os_cosmo_nt_arm64.go` | **The Win32 layer is implemented on arm64; the boot path and the netpoller are not.** `sys_cosmo_nt_arm64.s` supplies the AAPCS64 `ntcall6`/`ntcall10` trampolines (TEB via `R18_PLATFORM`, the `SetLastError(0)` bracket), the thread start, the VEH/VCH thunks and the console/signal trampolines, and `os_cosmo_nt_ctx_arm64.go` supplies the `ARM64_NT_CONTEXT` record and the five operations preemption performs on it. Every file that was `cosmo && amd64` is now `cosmo`, so `ntFutexsleep`, `ntNewosproc`, `ntVirtualAlloc`/`Free`, `ntSigaction`, `ntGoenvs`, `ntPreemptM`, `ntMinitThread`, `ntInitConsoleCtrl` and the profiler are live code on both arches. Sourcing is upstream's own windows/arm64 port (`runtime/sys_windows_arm64.s`, `internal/runtime/syscall/windows/defs_windows_arm64.go`, `signal_windows_arm64.go`). Still throwing: `netpollinitNT` and friends, because they sit on the syscall-emulation layer, which is written against Linux **amd64** numbering — arm64 has no bare `stat`/`lstat`/`open`, its `struct stat` is 144 bytes rather than 128, and `O_DIRECTORY` differs. That split is its own job. **Still unreachable, and still enforced rather than asserted**: `iswindows` remains a constant `false` on arm64, because no APE boot stub sets `__hostos` there (`rt0_cosmo_nt_amd64.s` has no arm64 twin and the APE has no arm64 PE header), and `TestPlatformTableIsClosed` (`cmd/internal/cosmoape`, run unfiltered by the ubuntu leg) fails if a windows row for a non-amd64 arch is added to the linker's boot-mechanism table without the runtime to back it. |
| 3 | termios round trip | The conversion had never met a driver: the tables are unit-tested arithmetic, and the termios probe only proves the request reaches the kernel. Fixed on this branch, unconfirmed: the `pty` check opens `/dev/ptmx`, sets raw mode and reads it back, so it needs no controlling terminal. |

## 4. The distribution suite under the cosmo port

`run.bash` runs `dist test` for the cosmo port on every CI leg. These packages are still red on a Linux host.

| # | Package | Behavior |
|---|---------|----------|
| 1 | `cmd/addr2line`, `cmd/link`, `cmd/compile/internal/ssa`, `runtime` (`TestUnsafePoint`) | Each opens a linked binary with `debug/elf` or `go tool nm` and gets `bad magic number '[77 90 113 70]'`, `no symbols`, or `dwarf too short`. Those bytes are `MZqF`: the APE header. `objfile.Open` reads the sidecar now, and `debug/elf` does not, so a test that opens the file itself still sees the container. |
| 2 | `cmd/link` (`TestELFHeadersSorted`, `TestIssue42396`) | Each asks for a build mode the port does not have, `pie` and `-race`, and reads the refusal as a failure rather than a skip. `testenv.GOOS` is the guard the objdump tests now use. |
| 3 | `cmd/compile/internal/test` (the PGO inlining family) | The inline decisions the golden output names do not match what the compiler produces here. Not yet diagnosed: this fork also adds loop-aware inlining. |
| 4 | `net/http/internal/http2` (`TestServer_Request_Connect_InvalidPath`) | Panics with "running on the wrong goroutine" inside a synctest bubble. Tests are parallel by default here, and synctest is what that meets. |
| 5 | `runtime` (`TestGoroutineProfile`) | `GoroutineProfile failed`, then SIGQUIT. Not diagnosed. |
| 7 | `cmd/go` (`TestLinkSysoFiles`, `TestScript`) | Not diagnosed. CLAUDE.md already records `list_symlink_issue35941` as red over the whole-repo vendor submodules, which is a different thing. |
| 6 | `os` (`TestExecutableDeleted`) | EBUSY on removing a running APE. This is a root-only sandbox artifact: as root the staging bootstrap bind-mounts the copy over the original path, and a mount point does not unlink. A CI runner is not root, and this passes there. |

## 5. Wordspam the hook does not yet scan

`dats/no-wordspam.dats` covers every markdown file this fork wrote and a handful of cosmo sources. One body stays outside it:

| # | Location | Behavior |
|---|----------|----------|
| 1 | `src/runtime/*cosmo*.go`, `src/internal/runtime/syscall/cosmo/*.go`, `src/syscall/*cosmo*.go` | About forty files carry a comment block past the twelve-line cap, up to 121 lines. Each needs the invariant kept and the narrative around it cut. |

## 6. NT gaps Windows cannot close

`prlimit64` is ENOSYS: Windows has no counterpart and upstream's own windows port does not expose it either. `fchmod`/`fchmodat` are a documented no-op after an existence check, and `fchown`/`fchownat` have no unix ownership to change. The reasoning is in `ntEmuFchmod`.


## 7. macOS gaps Apple cannot close

`Setresuid`, `Setresgid`, `Setfsuid`, `Setfsgid` and `mknodat` with a directory descriptor are ENOSYS: Apple has no counterpart. `Fchmodat` reports `EOPNOTSUPP` for `AT_SYMLINK_NOFOLLOW` on every host, because the Linux syscall takes no flags and one APE must not answer the same call differently per host.

Two Linux `Statfs_t` fields have no Apple source: `Type` carries Apple's own filesystem-type number, and `Namelen` stays zero rather than carrying a guess. `Utsname.Domainname` stays empty for the same reason.
