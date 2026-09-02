# Per-platform runtime status (GOOS=cosmo)

Windows status (2026-07-20, NT bring-up wave 3 COMPLETE plus the
LookPath fix - CI-verified by the full 47-check runtimeprobe gauntlet
on windows-latest, against
binaries built on all three platforms): stdout/stderr (console CP_UTF8+VT),
os.Args via GetCommandLineW, environment, os.Exit, VirtualAlloc memory,
CreateThread Ms, WaitOnAddress futexes, KUSER clocks, NumCPU; every
user-level syscall routes through an NT emulation dispatcher (Linux
numbers/errnos/structs in, Win32 out - src/runtime/os_cosmo_nt_sys.go)
covering process identity, ProcessPrng entropy, the whole file I/O
family with an fd table and a documented Linux<->Win32 path translation
(/tmp -> GetTempPathW, /c/... <-> C:\..., /dev/null -> NUL), getdents64
emulation (os.ReadDir/WalkDir/RemoveAll), working-directory round-trip,
os.Executable, and timers; os/exec (pipe2 over CreatePipe - blocking,
non-pollable on purpose - a posix_spawn-style CreateProcessW path with
upstream-ported quoting and env block, and wait4 packing the Linux
wait-status protocol: exit = code<<8, NTSTATUS crashes and encoded
signal deaths 0xC0DE0000|sig decode as Linux termination signals),
including exec.LookPath/exec.Command name resolution against the
HOST-format PATH (2026-07-20, src/os/exec/lp_cosmo.go: runtime host
switch; on NT a lp_windows.go port - ';' split, PATHEXT/.exe probing,
ErrDot semantics, case-INSENSITIVE PATH/PATHEXT env lookup since NT
blocks spell "Path" while cosmo's os.Getenv stays exact-case - plus
an extensionless-APE last resort; unix hosts keep verbatim lp_unix
behavior - see DEBUGGING.md 2026-07-20 NT LookPath section);
sockets over classic synchronous winsock (non-overlapped WSASocketW,
FIONBIO, AF_INET6 10<->23 and curated sockopt translation - SO_REUSEADDR
is swallowed for AF_UNIX because msafd accepts it and afunix.sys then
refuses bind - WSAE->errno map, SIO_UDP_CONNRESET disabled on UDP) with
a WSAPoll readiness netpoller (netpoll_aix.go's level-triggered two-lock
design; the wake channel is a connected loopback TCP pair because real
NT may drop loopback UDP datagrams - a lost wake stalls the poller;
pipes stay non-pollable/blocking on purpose); AF_UNIX pathname stream
sockets over afunix.sys (sun_path through the path layer; abstract
names refused EINVAL; wine's ws2_32 lacks AF_UNIX entirely, so wine
runs show exactly one red there while windows-latest proves it);
wave-3 socket growth: socketpair(2) over a loopback TCP pair dressed
as unnamed AF_UNIX, socket-kind dup(2), sendmsg/recvmsg + readv/
writev (net.Buffers) over WSASend/WSARecv, and SCM_RIGHTS fd passing
between cosmo processes (sender-push wire frame on the afunix
stream: WSADuplicateSocketW for sockets, OpenProcess+DuplicateHandle
for files/pipes, peer pid via SIO_AF_UNIX_GETPEERPID; pathname
AF_UNIX carriers only, same user, both ends must be cosmo binaries -
see DEBUGGING.md wave 3 item 2b for the honest limits); and
signals: VEH-based sigpanic (SIGSEGV recover works), self-signals
(kill/tkill with full delivery through sigtrampgo), os/signal Notify,
async preemption via SuspendThread/SetThreadContext injection
(preempt ~180ms on the CI runner, upstream preemptM semantics), signal
deaths encoded for the wait4 protocol, SIGPROF-parity CPU profiling
(runtime/pprof delivers real samples on NT: upstream os_windows.go's
profileLoop ported as a standing no-P M parked on a waitable timer,
SuspendThread under ntSuspendLock, direct sigprof calls - no signal
number anywhere), conhost control events remapped for unix parity -
CTRL_C -> SIGINT, CTRL_BREAK -> SIGQUIT (the goroutine-dump chord on
a wedged process), CTRL_CLOSE -> SIGHUP, LOGOFF/SHUTDOWN -> SIGTERM,
a deliberate divergence from upstream windows Go (which maps BREAK ->
SIGINT, CLOSE -> SIGTERM) - via an asm handler + relay M, and process
groups: SysProcAttr{Setpgid} spawns the child as its own group leader
(CREATE_NEW_PROCESS_GROUP) and kill(-pgid) delivers SIGQUIT
group-wide over GenerateConsoleCtrlEvent(CTRL_BREAK); the ctrlbreak
probe CI-proves the conhost-injected handler chain end to end.

File metadata followed (2026-09-02, the metadata wave): utimensat,
truncate, fchdir and linkat over SetFileTime, SetEndOfFile,
GetFinalPathNameByHandleW+SetCurrentDirectoryW and CreateHardLinkW, so
os.Chtimes, os.Truncate, os.File.Chdir and os.Link work here. The
runtimeprobe fsmeta check is a hard assertion on this host too;
docs/STUBS-INVENTORY.md section 5a lists what Windows still cannot
serve, all of it absent from upstream's own windows port as well.

Still missing on Windows: Windows/arm64 (the charter's step-one experiment
ran 2026-07-21: WoA x86-64 emulation is FAIL-to-boot - deterministic
pre-main SIGSEGV at 0x2000c9000; see DEBUGGING.md's wave-4 verdict
section - so native bring-up gains urgency), file/pipe dup(2)
(ENOSYS on purpose - socket dup works, and file/pipe fds still
transfer via SCM_RIGHTS), SCM_RIGHTS on socketpair ends (EOPNOTSUPP
by design - pair ends cannot cross processes),
off-host TCP (loopback sockets are CI-proven; the DNS half of the
2026-07-20 outbound-HTTPS report is fixed - a cosmo build takes
`dnsconfig_unix.go`, whose tag is `!windows`, so it read a
resolv.conf that does not exist on NT and fell back to querying
localhost, and the servers now come from iphlpapi's
GetNetworkParams instead, with runtimeprobe's `dns` check measuring
it on every runner - but nothing had got past DNS to attempt an
off-host connect, so TCP beyond loopback stays unproven either way
- see DEBUGGING.md's off-host HTTPS section; the trust store was the
same shape of gap one layer up, and is also fixed: every path in
crypto/x509's `root_cosmo.go` is a unix path, so the root pool came
back empty and each handshake reported an unknown authority, and the
roots now come from crypt32's ROOT store, with
testdata/runtimeprobe's `tls` check measuring a real handshake - see
DEBUGGING.md's trust-store section), and
real-keyboard/CTRL_CLOSE console coverage (the probe covers the
GenerateConsoleCtrlEvent-injected CTRL_BREAK chain; keyboard chords,
window close, LOGOFF/SHUTDOWN, and group-targeted CTRL_C stay
documented-not-asserted) - see DEBUGGING.md's NT wave sections for
the ladder, the forensics, and the wave-4 backlog.

macOS ARM64 status (2026-07-21): file I/O (create/read/write/stat/
rename/remove), directory listing (os.ReadDir/filepath.WalkDir/os.RemoveAll
via a getdents64 emulation over Apple's __getdirentries64),
getpid/getppid, NumCPU, the monotonic clock, timers (time.Sleep/Ticker/
After, context timeouts), TCP/UDP loopback sockets with deadlines,
unix-domain stream sockets (pathname addresses; the abstract namespace
is Linux-only and refused EINVAL), readv/writev (net.Buffers),
sendmsg/recvmsg with SCM_RIGHTS fd passing (2026-07-21:
msghdr/cmsghdr layouts differ - Linux 16-byte/8-aligned cmsg headers
vs Apple 12-byte/4-aligned - so the fixed-size msghdr re-shaping
lives in the nosplit dispatch layer while package syscall's darwin
branch repacks control buffers as ordinary Go; ReadMsgUnix/
WriteMsgUnix work, MSG_CMSG_CLOEXEC is emulated via fcntl,
truncation-dropped fds are closed never leaked, and the runtimeprobe
sendmsg/fdpass checks are mandatory on macOS - see DEBUGGING.md
2026-07-21), os/exec
(fork, pipes, execve, wait4 with Linux-numbered wait statuses),
os.Executable, argv/env, Getwd/Chdir, and SIGNALS all work (CI-verified
by the runtime probe on macos-latest): SIGSEGV -> sigpanic/recover,
os/signal Notify delivery, async preemption (SIGURG - tight loops no
longer hang GC/STW), and kill/raise, with full Linux<->Apple
signal-number and sigset translation at every darwin boundary (tables
in src/runtime/sigxlat_cosmo.go). SIGPROF CPU profiling works too
(2026-07-21): runtime/pprof and -test.cpuprofile deliver real samples
on macOS hosts - setitimer(ITIMER_PROF) via dlsym'd Apple libc
setitimer with the Linux<->Apple itimerval layout translated at the
boundary, SIGPROF riding the existing wave-9 signal machinery,
upstream-darwin attribution semantics; the pthread parking wrappers
record m.libcall* so samples inside pthread_cond_wait attribute to
the Go call site, and the runtimeprobe cpuprof check is mandatory on
macOS. SIGPIPE additionally stays suppressed
per-socket via SO_NOSIGPIPE, matching Go's EPIPE-error semantics. As of
wave 9 the darwin netpoller is a kqueue port of upstream
netpoll_kqueue.go (kqueue/kevent via dlsym) and M parking is upstream
os_darwin.go's pthread_mutex+pthread_cond design - this pair replaced
the poll(2)+self-pipe poller and dispatch-semaphore parking after the
waves-6..9 nondeterministic macOS CI wedge was root-caused (by in-CI
counter forensics, DEBUGGING.md wave 9) to XNU sporadically never
returning from a nonblocking read(2) on the poller's wakeup pipe.
The wave-9 "still missing on macOS hosts" backlog is now closed
(sendmsg/recvmsg and SIGPROF profiling were its last entries).

File metadata and system information followed (2026-09-02, the
metadata wave): fsync, truncate/ftruncate, chmod/fchmod/fchmodat,
chown/fchown/fchownat, fchdir, link/symlink, chtimes (utimensat),
mkfifo, statfs/fstatfs, uname, getrlimit/setrlimit (prlimit64),
get/setpriority, getpgid, get/setgroups, the uid/gid setters, chroot
and sendfile. Everything the syscall package exposes and Apple can
serve now works on macOS; docs/STUBS-INVENTORY.md section 6 lists what
each one needed and the few Apple genuinely lacks (setresuid/setresgid,
setfsuid/setfsgid, a directory-relative mknodat). The runtimeprobe
fsmeta/sysinfo/sendfile checks are mandatory on macOS.

The remaining known macOS gaps are AllThreadsSyscall (Linux-only
rt-signal machinery, unused by the stdlib on cosmo) and the
Intel-mac runtime bring-up below - see DEBUGGING.md.

**Variadic libc calls must pass their variadic arguments on the STACK
(2026-07-26).** arm64-apple diverges from AAPCS64 here even when
argument registers are free, so a variadic callee handed its argument
in a register reads uninitialized stack memory instead - and, the
value usually being a flag word, succeeds while doing something other
than what was asked. `fcntl(fd, F_SETFD, FD_CLOEXEC)` through the
fixed-argument trampoline set close-on-exec from stack garbage,
leaving descriptors unprotected perhaps a third of the time; that put
os/exec's child status pipe into the child and deadlocked any parent
whose child did not exit promptly - the long-standing "flaky" macOS
fdpass wedge. The same defect explains the F_DUPFD_CLOEXEC EINVAL and
O_CREAT modes taken from garbage. Use
`runtime.cosmoLibcCallVariadic1` / `darwin_call_v3` for any variadic
libc function (fcntl, open/openat with a mode, ioctl); never
`cosmoLibcCall6` or `darwin_call`. The runtimeprobe `cloexec` check
gates it. Full forensics: DEBUGGING.md 2026-07-26.

macOS Intel status: the dd-assimilated Mach-O is structurally correct as of
2026-07-02 (per-PT_LOAD segments with real protections and BSS, __PAGEZERO,
host-OS handoff in rcx - verified against the XNU loader's checks by cmd/link
unit tests and apetest). The syscall surface closed on 2026-09-02: the
metadata table, the XNU carry-flag error convention, Apple-to-Linux errno
numbering, the kqueue/kevent netpoller and hw.ncpu. Signal INSTALLATION
closed the same day: `darwinSigaction` translates the Linux `sigactiont` and
issues the raw `__sigaction` syscall with `runtime·cosmoXnuSigtramp` as its
`sa_tramp`, the trampoline libc supplies for the arm64 path and a raw caller
must supply itself. Before that the asm branch returned success without
installing anything, so no handler the runtime set was ever present. What is
still incomplete is the sigset-width bridge in `darwinSigprocmask`: the mask
path translates `how` and then hands a kernel that reads a 4-byte Apple
sigset the 8-byte Linux one. Thread creation joined the closed list via
bsdthread_create, and parking via a polled wait since XNU has no futex.
There is still no
Intel-mac CI runner, so end-to-end execution there is UNTESTED. Do not claim macOS Intel "works" until the runtime bring-up lands
and is verified on real hardware.
