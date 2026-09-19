# Per-platform runtime status (GOOS=cosmo)

Windows status (2026-07-20, NT bring-up wave 3 COMPLETE plus the LookPath fix - CI-verified by the runtimeprobe gauntlet on windows-latest, against binaries built on all three platforms).

Basics: stdout/stderr (console CP_UTF8+VT), os.Args via GetCommandLineW, environment, os.Exit, VirtualAlloc memory, CreateThread Ms, WaitOnAddress futexes, KUSER clocks, NumCPU. Every user-level syscall routes through an NT emulation dispatcher (Linux numbers/errnos/structs in, Win32 out - src/runtime/os_cosmo_nt_sys.go) covering process identity, ProcessPrng entropy, the whole file I/O family with an fd table.

Os/exec: pipe2 over CreatePipe, blocking and non-pollable on purpose. A posix_spawn-style CreateProcessW path carries upstream-ported quoting and env block. The wait4 call packs the Linux wait-status protocol, where exit is code<<8. NTSTATUS crashes and encoded signal deaths 0xC0DE0000|sig decode as Linux termination signals. exec.LookPath/exec.Command resolve names against the HOST-format PATH (src/os/exec/lp_cosmo.go: runtime host switch. On NT a lp_windows.go port - '.' split, PATHEXT/.exe probing, ErrDot semantics, case-INSENSITIVE PATH/PATHEXT env lookup since NT blocks spell "Path" while cosmo's os.Getenv stays exact-case. Unix hosts keep verbatim lp_unix behavior).

Sockets over classic synchronous winsock (non-overlapped WSASocketW, FIONBIO, AF_INET6 10<->23 and curated sockopt translation - SO_REUSEADDR is swallowed for AF_UNIX because msafd accepts it and afunix.sys then refuses bind - WSAE->errno map, SIO_UDP_CONNRESET disabled on UDP) with a WSAPoll readiness netpoller (netpoll_aix.go's level-triggered two-lock design. The wake channel is a connected loopback TCP pair because real NT may drop loopback UDP datagrams - a lost wake stalls the poller. Pipes stay non-pollable/blocking on purpose).

AF_UNIX pathname stream sockets over afunix.sys (sun_path through the path layer. Abstract names refused EINVAL. Wine's ws2_32 lacks AF_UNIX entirely, so wine runs show exactly one red there while windows-latest proves it). Wave-3 socket growth: socketpair(2) over a loopback TCP pair dressed as unnamed AF_UNIX, socket-kind dup(2), sendmsg/recvmsg + readv/writev (net.Buffers) over WSASend/WSARecv. Pathname AF_UNIX carriers only, same user, both ends must be cosmo binaries.

Signals: VEH-based sigpanic (SIGSEGV recover works), self-signals (kill/tkill with full delivery through sigtrampgo), os/signal Notify, async preemption via SuspendThread/SetThreadContext injection (preempt ~180ms on the CI runner, upstream preemptM semantics), signal deaths encoded for the wait4 protocol, SIGPROF-parity CPU profiling. The ctrlbreak probe CI-proves the conhost-injected handler chain end to end.

File metadata followed (2026-09-02, the metadata wave): utimensat, truncate, fchdir and linkat over SetFileTime, SetEndOfFile, GetFinalPathNameByHandleW+SetCurrentDirectoryW and CreateHardLinkW, so os.Chtimes, os.Truncate, os.File.Chdir and os.Link work here. The runtimeprobe fsmeta check is a hard assertion on this host too.

Symlinks and the read-only attribute followed (2026-09-16, `os_cosmo_nt_link.go`). A symlink is created over CreateSymbolicLinkW. Readlink reads the substitute name through FSCTL_GET_REPARSE_POINT. Lstat and getdents64 report a name-surrogate reparse point as a link. Every fd kind duplicates over DuplicateHandle, through dup, dup2 and dup3. Unlink clears FILE_ATTRIBUTE_READONLY before DeleteFileW and takes a directory symlink. Chmod carries the owner's write bit as that attribute, and stat reports it as 0555 against 0755. The temp directory is answered in the /c/ spelling Getwd and Executable use, so a path built under it compares equal to one read back.

What Windows cannot serve, all of it absent from upstream's own windows port as well: `prlimit64` is ENOSYS, because Windows has no counterpart. `fchmod` and `fchmodat` carry one bit, and `fchown`/`fchownat` have no unix ownership to change.

The NT suite's red is measured again on run 35408440298. That is the last run the expired waiver covered. The second shard is clean. The first shard fails `cmd/go`, `cmd/go/internal/work`, `os/exec`, `net/http/cgi`, `internal/syscall/windows`, `cmd/cgo/internal/testgodefs` and `cmd/cgo/internal/swig`.

The largest group is one failure mode. A child process exits `0xc0000135`, which is STATUS_DLL_NOT_FOUND. It prints nothing at all. `autocgo` shows it cleanly. `go env CGO_ENABLED` answers `1`. The script then sets `PATH=$GOROOT/bin`, and the same command dies. `gotoolchain_issue66175`, `mod_doc_path` and `net/http/cgi`'s TestEnvOverride strip the environment the same way.

**Those children are not APEs.** `run.bash` and `run.bat` pin `GOOS` and `GOARCH` to the host values. So `dist test` on each leg tests that host's own port. The windows leg tests `GOOS=windows`. The binaries it builds, copies and starts are native PEs from this fork's linker. `os/exec`'s TestCommand comes from `lp_windows_test.go`, which a cosmo build does not compile at all. The cosmo port's own coverage is `dats/checks/cosmo-tests.dats`, on the ubuntu leg.

Some readings of the failure are measured and wrong. Env lookup on NT is case-insensitive, so the all-caps `SYSTEMROOT` key the cmd/go script harness copies does reach the child. The binaries import `kernel32.dll` and nothing else, the windows-targeted test binaries included. No DLL is missing from the stripped PATH. `os/exec`'s `addCriticalEnv` reads `runtime.GOOS`, which names the host here. It does put SYSTEMROOT back. `ntSpawn` passes CREATE_UNICODE_ENVIRONMENT with its UTF-16 block.

The host refuses nothing. `dats/test/nt-strippedpath.ps1` copies a system binary the way os/exec's TestCommand copies its own, starts it from that copy, and asserts the exit code. The runner answers 0 under a dot PATH and under an empty one. So a stripped PATH stops no program there by itself. The binary decides it. The same check copies the gofmt beside the upstream go the workflow installs. It asks that binary the same question. The check copies gofmt and not go. A go command outside its own tree cannot find GOROOT. It fails whatever the PATH holds. That result describes the copy, not the host. The gofmt binary carries the same kernel32-only import shape. It reads its arguments and needs no tree.

Wine reproduces none of it. It runs a cosmo APE as an NT host. It also runs the windows-targeted os/exec test binary green, TestCommand included. A child starts there under an empty environment, from a copy, and under a PATH holding no Windows directory. The remaining work needs the windows-latest runner. A stdlib test branches on `runtime.GOOS`. On cosmo that is a readonly var naming the HOST. An NT runner therefore takes the windows expectations. The package under it compiled `path_unix.go`, whose tag is `unix || (js && wasm) || wasip1`, and cosmo is a unix. The test asks a unix build for windows behavior.

One such host switch is gone rather than fixed: `os.UserCacheDir` answers `$XDG_CACHE_HOME`, else `$HOME/.cache`, on every host. Upstream picks `$HOME/Library/Caches` on darwin, `%LocalAppData%` on NT and `$home/lib/cache` on plan9, which gives one binary a different cache on each machine it runs on. `go env GOCACHE` follows it, so the build cache lands in `~/.cache/go-build` everywhere.

`path/filepath` carries both readings in one file. `TestIsLocal` asks `testenv.GOOS`, the build-target constant. It appends nothing and passes. `TestLocalize` switches on `runtime.GOOS`, appends `winlocalizetests`, and fails on NUL. The durable fix is a host switch inside the package. `os/exec`'s `lp_cosmo.go` is the shape to copy. Widening a build tag is not the fix.

The remainder of that red, by cause:

| what the failures say | what it is |
|---|---|
| `got broken pipe, expected errno 232` | NT answers a closed pipe with ERROR_NO_DATA, and cosmo maps it to EPIPE |
| `protocol not available` | sockopts NT does not serve |
| `The system cannot find the path specified` | the path layer |
| `unknown directive "MZqFpD='"` | a tool parses an APE's own header as source |
| `function not implemented` | ENOSYS stubs |
| `Mode = "-rwx------", want "-rw-------"` | NT has no unix mode bits |
| `os` and `os/exec` at 600s | neither fails. Both hang to the timeout |

Still missing on Windows: Windows/arm64 (the charter's step-one experiment ran 2026-07-21: WoA x86-64 emulation is FAIL-to-boot - deterministic pre-main SIGSEGV at 0x2000c9000. so native bring-up gains urgency). The DNS half of the 2026-07-20 outbound-HTTPS report is fixed - a cosmo build takes `dnsconfig_unix.go`, whose tag is `!windows`. It read a resolv.conf. The trust store was the same shape of gap one layer up, and is also fixed. Every path in crypto/x509's `root_cosmo.go` is a unix. Keyboard chords, window close, LOGOFF/SHUTDOWN, and group-targeted CTRL_C stay documented-not-asserted).

macOS ARM64 status (2026-07-21): file I/O (create/read/write/stat/rename/remove), directory listing (os.ReadDir/filepath.WalkDir/os.RemoveAll via a getdents64 emulation over Apple's __getdirentries64), getpid/getppid, NumCPU, the monotonic clock, timers (time.Sleep/Ticker/After, context timeouts), TCP/UDP loopback sockets with deadlines, unix-domain stream (the abstract namespace is Linux-only and refused EINVAL), readv/writev (net.Buffers).

sendmsg/recvmsg with SCM_RIGHTS fd passing: msghdr/cmsghdr layouts differ - Linux 16-byte/8-aligned cmsg headers against Apple's. ReadMsgUnix/WriteMsgUnix work, MSG_CMSG_CLOEXEC is emulated via fcntl, truncation-dropped fds are closed never leaked, and the runtimeprobe sendmsg/fdpass checks are mandatory on macOS.

SIGPROF CPU profiling: runtime/pprof and -test.cpuprofile deliver real samples on macOS hosts, over setitimer(ITIMER_PROF) through dlsym. The pthread parking wrappers record m.libcall* so samples inside pthread_cond_wait attribute to the Go call site, and the runtimeprobe cpuprof check is mandatory here. SIGPIPE stays suppressed per-socket via SO_NOSIGPIPE, matching Go's EPIPE-error semantics.

As of wave 9 the darwin netpoller is a kqueue port of upstream netpoll_kqueue.go (kqueue/kevent via dlsym) and M parking is upstream os_darwin.go's pthread_mutex+pthread_cond design. The wave-9 "still missing on macOS hosts" backlog is closed - sendmsg/recvmsg and SIGPROF profiling were its last entries.

A profile taken on an arm64 macOS host names its own mapping one page above the image base. `cmd/pprof -disasm` then resolves no function.

`objTool.Open` computes `offset = mappingStart - loadAddress`. loadAddress is the first executable PT_LOAD's vaddr. On linux/amd64 both values are 0x100000000. The offset is 0 there. On darwin/arm64 the mapping reads 0x800001000 against 0x800000000. Every address moves by 0x1000. `main.main` matches nothing.

The samples are correct. The profile still symbolizes main.main through the pclntab. Linux reads the real base from /proc/self/maps. The darwin path reports the text start instead. cmd/pprof's TestDisasm fails on that leg alone.

File metadata and system information followed (2026-09-02, the metadata wave): fsync, truncate/ftruncate, chmod/fchmod/fchmodat, chown/fchown/fchownat, fchdir, link/symlink, chtimes (utimensat), mkfifo, statfs/fstatfs, uname, getrlimit/setrlimit (prlimit64), get/setpriority, getpgid, get/setgroups, the. Everything the syscall package exposes and Apple can serve now works on macOS. The runtimeprobe fsmeta/sysinfo/sendfile checks are mandatory on macOS. A raw `Syscall` statfs or fstatfs with a Linux `Statfs_t` works too. That is the call golang.org/x/sys/unix makes. `Syscall` converts the struct before it enters the syscall.

What Apple cannot serve: `Setresuid`, `Setresgid`, `Setfsuid`, `Setfsgid` and `mknodat` with a directory descriptor are ENOSYS, because Apple has no counterpart. `Fchmodat` reports `EOPNOTSUPP` for `AT_SYMLINK_NOFOLLOW` on every host: the Linux syscall takes no flags, and one APE must not answer one call two ways. Two Linux `Statfs_t` fields have no Apple source - `Type` carries Apple's own filesystem-type number, and `Namelen` stays zero rather than carrying a guess. `Utsname.Domainname` stays empty for the same reason.

Locking, durability and the terminal followed (2026-09-06): flock, fdatasync, sync, getrusage, gettimeofday, and ioctl - the window-size and job-control requests plus the termios family (TCGETS/TCSETS/TCSETSW/TCSETSF) over Apple's TIOCGETA/TIOCSETA, so a program can put a terminal into raw mode here. termios converts the struct as well as the request: Apple has 64-bit flag words against 32, twenty control characters against nineteen at different indices, speeds in their own fields rather than inside c_cflag, and colliding bits (Linux IXON is Apple IXOFF). Windows serves flock over LockFileEx, and statfs/fstatfs over GetVolumePathNameW, GetDiskFreeSpaceW, GetDiskFreeSpaceExW and GetVolumeInformationW.

Runtimeprobe checks: flock, durable, rusage, ioctl, termios, volume. The ubuntu leg's unit tests pin the termios translation, but no CI runner has a terminal, so its round trip has never run against a live driver - untested, not unbuilt.

The remaining known macOS gap is AllThreadsSyscall (Linux-only rt-signal machinery, unused by the stdlib on cosmo).

**Variadic libc calls must pass their variadic arguments on the STACK (2026-07-26).** arm64-apple diverges from AAPCS64 here even when argument registers are free, so. `fcntl(fd, F_SETFD, FD_CLOEXEC)` through the fixed-argument trampoline set close-on-exec from stack garbage, leaving descriptors unprotected perhaps a third of the time. That put os/exec's child status pipe into the child and deadlocked any parent whose child did not exit promptly - the long-standing "flaky" macOS fdpass. The same defect explains the F_DUPFD_CLOEXEC EINVAL and O_CREAT modes taken from garbage. Use `runtime.cosmoLibcCallVariadic1` / `darwin_call_v3` for any variadic libc function (fcntl, open/openat with a mode, ioctl). Never `cosmoLibcCall6` or `darwin_call`. The runtimeprobe `cloexec` check gates it.

macOS Intel status: not a platform this toolchain emits. `cosmoape`'s table drops darwin/amd64, so the linker writes no Mach-O header and `GOCOSMOPLATFORMS=darwin/amd64` is refused. XNU reads the Mach-O header at offset 0, which an APE cannot carry there. Starting on that host needs a copy of the whole program in a writable place, and every platform here starts without writing anything. The runtime's amd64 XNU surface stays in the tree and stays untested. Bringing the platform back means answering the offset-0 problem, not just adding a table row.

Signal installation closed the same day. `darwinSigaction` translates the Linux `sigactiont` and issues the raw `__sigaction` syscall with `runtime·cosmoXnuSigtramp` as its `sa_tramp`. The `syscall` package's own `rt_sigaction` emulation (`internal/runtime/syscall/cosmo`) carries its own trampoline. `darwinSigprocmask` translates `how` and bridges the sigset width (8-byte Linux, 4-byte Apple) in both directions, signal by signal. Thread creation joined the closed list via bsdthread_create, and parking via a polled wait since XNU has no futex.

Signal delivery is closed too: `sigctxt` dispatches on the host and reads XNU's `user_ucontext64`, whose mcontext sits behind a pointer at offset 48. Apple's SIGFPE `si_code` values are remapped to Linux's for sigpanic.

No Intel-mac CI runner exists. Nothing there has ever executed. Do not claim macOS Intel "works".
