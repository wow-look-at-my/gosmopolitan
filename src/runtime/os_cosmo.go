// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// sigPerThreadSyscall is the same signal (SIGSETXID) used by glibc for
// per-thread syscalls. We use it for the same purpose.
const sigPerThreadSyscall = _SIGRTMIN + 1

// Cosmopolitan uses Linux futex.
//
//	futexsleep(uint32 *addr, uint32 val)
//	futexwakeup(uint32 *addr)
//
// Futexsleep atomically checks if *addr == val and if so, sleeps on addr.
// Futexwakeup wakes up threads sleeping on addr.
// Futexsleep is allowed to wake up spuriously.

// Atomically,
//
//	if(*addr == val) sleep
//
// Might be woken up spuriously; that's allowed.
// Don't sleep longer than ns; ns < 0 means forever.
//
//go:nosplit
func futexsleep(addr *uint32, val uint32, ns int64) {
	if iswindows() {
		// NT: WaitOnAddress has the same compare-and-wait,
		// spurious-wakeup-permitted semantics as futex.
		ntFutexsleep(addr, val, ns)
		return
	}
	if ns < 0 {
		futex(unsafe.Pointer(addr), _FUTEX_WAIT_PRIVATE, val, nil, nil, 0)
		return
	}

	var ts timespec
	ts.setNsec(ns)
	futex(unsafe.Pointer(addr), _FUTEX_WAIT_PRIVATE, val, &ts, nil, 0)
}

// If any procs are sleeping on addr, wake up at most cnt.
//
//go:nosplit
func futexwakeup(addr *uint32, cnt uint32) {
	if iswindows() {
		// Every caller passes cnt==1 (see the wave-1 design in
		// DEBUGGING.md), so WakeByAddressSingle suffices.
		ntFutexwakeup(addr)
		return
	}
	ret := futex(unsafe.Pointer(addr), _FUTEX_WAKE_PRIVATE, cnt, nil, nil, 0)
	if ret >= 0 {
		return
	}

	systemstack(func() {
		print("futexwakeup addr=", addr, " returned ", ret, "\n")
	})

	*(*int32)(unsafe.Pointer(uintptr(0x1006))) = 0x1006
}

func getCPUCount() int32 {
	if iswindows() {
		// GetSystemInfo through the NT table resolved at osArchInit
		// (which osinit runs before getCPUCount, deliberately).
		return ntNumCPU()
	}
	if isdarwin() {
		// sched_getaffinity is Linux-only; on macOS ask the host
		// via sysctl (arm64 Syslib). Without this every macOS run
		// was pinned to GOMAXPROCS=1.
		if n := cosmoDarwinNumCPU(); n > 0 {
			return n
		}
		return 1
	}
	// Use a conservative default. On Cosmopolitan, the actual CPU count
	// is determined at runtime based on the host OS.
	const maxCPUs = 64 * 1024
	var buf [maxCPUs / 8]byte
	r := sched_getaffinity(0, unsafe.Sizeof(buf), &buf[0])
	if r < 0 {
		return 1
	}
	n := int32(0)
	for _, v := range buf[:r] {
		for v != 0 {
			n += int32(v & 1)
			v >>= 1
		}
	}
	if n == 0 {
		n = 1
	}
	return n
}

// cloneFlags for creating new threads
const cloneFlags = _CLONE_VM | /* share memory */
	_CLONE_FS | /* share cwd, etc */
	_CLONE_FILES | /* share fd table */
	_CLONE_SIGHAND | /* share sig handler table */
	_CLONE_SYSVSEM | /* share SysV semaphore undo lists */
	_CLONE_THREAD /* revisit - okay for now */

//go:noescape
func clone(flags int32, stk, mp, gp, fn unsafe.Pointer) int32

// May run with m.p==nil, so write barriers are not allowed.
//
//go:nowritebarrier
func newosproc(mp *m) {
	if iswindows() {
		// CreateThread; no sigprocmask bracketing (signals are
		// inert on NT in wave 1).
		ntNewosproc(mp)
		return
	}
	stk := unsafe.Pointer(mp.g0.stack.hi)
	if false {
		print("newosproc stk=", stk, " m=", mp, " g=", mp.g0, " clone=", abi.FuncPCABI0(clone), " id=", mp.id, " ostk=", &mp, "\n")
	}

	// Disable signals during clone, so that the new thread starts
	// with signals disabled. It will enable them in minit.
	var oset sigset
	sigprocmask(_SIG_SETMASK, &sigset_all, &oset)
	ret := retryOnEAGAIN(func() int32 {
		r := clone(cloneFlags, stk, unsafe.Pointer(mp), unsafe.Pointer(mp.g0), unsafe.Pointer(abi.FuncPCABI0(mstart)))
		if r >= 0 {
			return 0
		}
		return -r
	})
	sigprocmask(_SIG_SETMASK, &oset, nil)

	if ret != 0 {
		print("runtime: failed to create new OS thread (have ", mcount(), " already; errno=", ret, ")\n")
		if ret == _EAGAIN {
			println("runtime: may need to increase max user processes (ulimit -u)")
		}
		throw("newosproc")
	}
}

// Version of newosproc that doesn't require a valid G.
//
//go:nosplit
func newosproc0(stacksize uintptr, fn unsafe.Pointer) {
	stack := sysAlloc(stacksize, &memstats.stacks_sys, "OS thread stack")
	if stack == nil {
		writeErrStr(failallocatestack)
		exit(1)
	}
	ret := clone(cloneFlags, unsafe.Pointer(uintptr(stack)+stacksize), nil, nil, fn)
	if ret < 0 {
		writeErrStr(failthreadcreate)
		exit(1)
	}
}

const (
	_AT_NULL   = 0 // End of vector
	_AT_PAGESZ = 6 // System physical page size
	_AT_RANDOM = 25
	_AT_HWCAP  = 16
	_AT_HWCAP2 = 26
	_AT_SECURE = 23
)

var procAuxv = []byte("/proc/self/auxv\x00")

var addrspace_vec [1]byte

func mincore(addr unsafe.Pointer, n uintptr, dst *byte) int32

var auxvreadbuf [128]uintptr

func sysargs(argc int32, argv **byte) {
	n := argc + 1

	// skip over argv, envp to get to auxv
	for argv_index(argv, n) != nil {
		n++
	}

	// skip NULL separator
	n++

	// now argv+n is auxv
	auxvp := (*[1 << 28]uintptr)(add(unsafe.Pointer(argv), uintptr(n)*goarch.PtrSize))

	if pairs := sysauxv(auxvp[:]); pairs != 0 {
		auxv = auxvp[: pairs*2 : pairs*2]
		return
	}
	if iswindows() {
		// The NT boot stub always fabricates a complete auxv
		// (AT_PAGESZ at minimum), so this point should be
		// unreachable; guard anyway because both fallbacks below
		// are Linux syscall paths (open /proc/self/auxv, and an
		// mmap+mincore page-size probe).
		return
	}
	// Fall back to /proc/self/auxv.
	fd := open(&procAuxv[0], 0 /* O_RDONLY */, 0)
	if fd < 0 {
		// On some platforms, /proc might not be available.
		// Try to detect page size using mincore.
		const size = 256 << 10
		p, err := mmap(nil, size, _PROT_READ|_PROT_WRITE, _MAP_ANON|_MAP_PRIVATE, -1, 0)
		if err != 0 {
			return
		}
		var n uintptr
		for n = 4 << 10; n < size; n <<= 1 {
			err := mincore(unsafe.Pointer(uintptr(p)+n), 1, &addrspace_vec[0])
			if err == 0 {
				physPageSize = n
				break
			}
		}
		if physPageSize == 0 {
			physPageSize = size
		}
		munmap(p, size)
		return
	}

	n = read(fd, noescape(unsafe.Pointer(&auxvreadbuf[0])), int32(unsafe.Sizeof(auxvreadbuf)))
	closefd(fd)
	if n < 0 {
		return
	}
	auxvreadbuf[len(auxvreadbuf)-2] = _AT_NULL
	pairs := sysauxv(auxvreadbuf[:])
	auxv = auxvreadbuf[: pairs*2 : pairs*2]
}

var secureMode bool

func sysauxv(auxv []uintptr) (pairs int) {
	var i int
	for ; auxv[i] != _AT_NULL; i += 2 {
		tag, val := auxv[i], auxv[i+1]
		switch tag {
		case _AT_RANDOM:
			startupRand = (*[16]byte)(unsafe.Pointer(val))[:]
		case _AT_PAGESZ:
			physPageSize = val
		case _AT_SECURE:
			secureMode = val == 1
		}
		archauxv(tag, val)
	}
	return i / 2
}

func osinit() {
	osArchInit()
	numCPUStartup = getCPUCount()
}

var urandom_dev = []byte("/dev/urandom\x00")

func readRandom(r []byte) int {
	if iswindows() {
		// ProcessPrng (or RtlGenRandom), resolved at osArchInit;
		// returns 0 when neither is available, selecting the
		// readTimeRandom fallback. (Boot hash seeds additionally
		// come from startupRand, which ntBootInit upgrades to
		// ProcessPrng output before randinit runs.)
		return ntReadRandom(r)
	}
	fd := open(&urandom_dev[0], 0 /* O_RDONLY */, 0)
	n := read(fd, unsafe.Pointer(&r[0]), int32(len(r)))
	closefd(fd)
	return int(n)
}

func goenvs() {
	if iswindows() {
		// The boot block's envp is empty on NT; the real environment
		// comes from GetEnvironmentStringsW (os_cosmo_nt.go).
		ntGoenvs()
		return
	}
	goenvs_unix()
}

// cosmoMstartm0 is the fork's mstartm0 hook (proc.go): on NT hosts it
// parks the console-control relay M and registers the ctrl handler
// (os_cosmo_nt_preempt.go). It must run this late - not in goenvs,
// where upstream windows registers its handler - because the relay
// needs newm, and allocm borrows the caller's P for its allocations:
// m0 only acquires p0 in procresize, at the END of schedinit.
// mstartm0 is the first m0 code after schedinit (its newextram
// precedent allocates the same way).
func cosmoMstartm0() {
	if iswindows() {
		ntInitConsoleCtrl()
	}
}

// Called to do synchronous initialization of Go code built with
// -buildmode=c-archive or -buildmode=c-shared.
// None of the Go runtime is initialized.
//
//go:nosplit
//go:nowritebarrierrec
func libpreinit() {
	initsig(true)
}

// Called to initialize a new m (including the bootstrap m).
// Called on the parent thread (main thread in case of bootstrap), can allocate memory.
func mpreinit(mp *m) {
	mp.gsignal = malg(32 * 1024)
	mp.gsignal.m = mp
}

func gettid() uint32

// Called to initialize a new m (including the bootstrap m).
// Called on the new thread, cannot allocate memory.
func minit() {
	minitSignals()
	if iswindows() {
		// Duplicate this thread's handle into m.thread so
		// ntPreemptM (async preemption) can address it
		// (os_cosmo_nt_preempt.go; arm64 stub is unreachable).
		ntMinitThread()
	}
	// minitProcid is per-arch: on macOS hosts (arm64) procid must hold
	// the FULL pthread_t for pthread_kill - gettid's uint32 return
	// would truncate the pointer; on Linux hosts it is the tid.
	getg().m.procid = minitProcid()
}

// Called from dropm and mexit to undo the effect of an minit.
//
//go:nosplit
func unminit() {
	unminitSignals()
	if iswindows() {
		// Close m.thread under threadLock: from here on ntPreemptM
		// treats this M as unpreemptible instead of suspending a
		// dying thread.
		ntUnminitThread()
	}
	getg().m.procid = 0
}

// Called from mexit, but not from dropm, to undo the effect of thread-owned
// resources in minit, semacreate, or elsewhere. Do not take locks after calling this.
//
// This always runs without a P, so //go:nowritebarrierrec is required.
//
//go:nowritebarrierrec
func mdestroy(mp *m) {
}

func sigreturn__sigaction()
func sigtramp()
func cgoSigtramp()

// sigaltstack is per-arch: on arm64 a Go host dispatcher
// (signal_cosmo_xnu.go) that translates Apple's stack_t on XNU hosts,
// on amd64 assembly (macOS-Intel runtime bring-up pending).

//go:noescape
func setitimer(mode int32, new, old *itimerval)

//go:noescape
func rtsigprocmask(how int32, new, old *sigset, size int32)

//go:nosplit
//go:nowritebarrierrec
func sigprocmask(how int32, new, old *sigset) {
	if isdarwin() && GOARCH == "arm64" {
		// Apple `how` values, sigset width and signal numbering all
		// differ; darwinSigprocmask (signal_cosmo_xnu.go) translates.
		// amd64 stays on its raw-XNU asm branch until the Intel-mac
		// runtime bring-up.
		darwinSigprocmask(how, new, old)
		return
	}
	rtsigprocmask(how, new, old, int32(unsafe.Sizeof(*new)))
}

func raise(sig uint32)
func raiseproc(sig uint32)

//go:noescape
func sched_getaffinity(pid, len uintptr, buf *byte) int32
func osyield()

//go:nosplit
func osyield_no_g() {
	osyield()
}

// pipe2 is per-arch: assembly on amd64 (os_cosmo_amd64.go), a Go
// host-dispatching implementation on arm64 (os_cosmo_arm64.go).

// fcntl is defined in fcntl_cosmo_amd64.go and fcntl_cosmo_arm64.go

//go:nosplit
//go:nowritebarrierrec
func setsig(i uint32, fn uintptr) {
	var sa sigactiont
	sa.sa_flags = _SA_SIGINFO | _SA_ONSTACK | _SA_RESTORER | _SA_RESTART
	sigfillset(&sa.sa_mask)
	if GOARCH == "386" || GOARCH == "amd64" {
		sa.sa_restorer = abi.FuncPCABI0(sigreturn__sigaction)
	}
	if fn == abi.FuncPCABIInternal(sighandler) {
		if iscgo {
			fn = abi.FuncPCABI0(cgoSigtramp)
		} else {
			fn = abi.FuncPCABI0(sigtramp)
		}
	}
	sa.sa_handler = fn
	sigaction(i, &sa, nil)
}

//go:nosplit
//go:nowritebarrierrec
func setsigstack(i uint32) {
	var sa sigactiont
	sigaction(i, nil, &sa)
	if sa.sa_flags&_SA_ONSTACK != 0 {
		return
	}
	sa.sa_flags |= _SA_ONSTACK
	sigaction(i, &sa, nil)
}

//go:nosplit
//go:nowritebarrierrec
func getsig(i uint32) uintptr {
	var sa sigactiont
	sigaction(i, nil, &sa)
	return sa.sa_handler
}

// setSignalstackSP sets the ss_sp field of a stackt.
//
//go:nosplit
func setSignalstackSP(s *stackt, sp uintptr) {
	*(*uintptr)(unsafe.Pointer(&s.ss_sp)) = sp
}

// fixsigcode is defined per-arch: signal_cosmo_amd64.go (no-op) and
// signal_cosmo_arm64.go (darwin SIGTRAP correction).

// sysSigaction calls the rt_sigaction system call (Linux hosts) or the
// Apple sigaction translation layer (XNU hosts, arm64).
//
//go:nosplit
func sysSigaction(sig uint32, new, old *sigactiont) {
	var ret int32
	if iswindows() {
		// NT: there is no kernel-side sigaction; the runtime records
		// handler state itself and self-directed delivery consults
		// the record (ntSigActs/ntKillSelf, os_cosmo_nt_sig.go).
		ret = ntSigaction(sig, new, old)
	} else if isdarwin() && GOARCH == "arm64" {
		ret = darwinSigaction(sig, new, old)
	} else {
		ret = rt_sigaction(uintptr(sig), new, old, unsafe.Sizeof(sigactiont{}.sa_mask))
	}
	if ret != 0 {
		if sig != 32 && sig != 33 && sig != 64 {
			systemstack(func() {
				throw("sigaction failed")
			})
		}
	}
}

// rt_sigaction is implemented in assembly.
//
//go:noescape
func rt_sigaction(sig uintptr, new, old *sigactiont, size uintptr) int32

//go:nosplit
func fixSigactionForCgo(new *sigactiont) {
	if GOARCH == "386" && new != nil {
		new.sa_flags &^= _SA_RESTORER
		new.sa_restorer = 0
	}
}

func getpid() int
func tgkill(tgid, tid, sig int)

// signalM sends a signal to mp. sig is a LINUX signal number; the
// darwin path (per-arch darwinSignalM) translates it and signals the
// thread via pthread_kill with the full pthread_t from m.procid.
func signalM(mp *m, sig int) {
	if iswindows() {
		// NT (wave 2 chunk D2): there is no cross-thread signal
		// delivery on NT; the only signal the runtime ever sends
		// another M is the scheduler's preemption request (grep:
		// preemptM in signal_unix.go is signalM's sole cosmo caller),
		// which the SuspendThread machinery services directly.
		// ntPreemptM acks preemptGen AND clears mp.signalPending on
		// every path - the unix preemptM wrapper's CAS gate stays
		// open. Anything else would be a new caller: drop it, as
		// wave 1 did.
		if sig == sigPreempt {
			ntPreemptM(mp)
		}
		return
	}
	if isdarwin() {
		darwinSignalM(mp, sig)
		return
	}
	tgkill(getpid(), int(mp.procid), sig)
}

// osPreemptExtEnter is called before entering external code that may
// call ExitProcess (NT hosts; a no-op elsewhere - preemption is
// signal-based on Linux/XNU and needs no bracket).
//
// This must be nosplit because it may be called from a syscall with
// untyped stack slots, so the stack must not be grown or scanned
// (upstream os_windows.go).
//
//go:nosplit
func osPreemptExtEnter(mp *m) {
	if !iswindows() {
		return
	}
	for !atomic.Cas(&mp.preemptExtLock, 0, 1) {
		// An asynchronous preemption is in progress. It's not safe
		// to enter external code because it may call ExitProcess and
		// deadlock with SuspendThread. Ideally we would do the
		// preemption ourselves, but can't since there may be untyped
		// syscall arguments on the stack. Instead, just wait and
		// encourage the SuspendThread APC to run. The preemption
		// should be done shortly.
		osyield()
	}
	// Asynchronous preemption is now blocked.
}

// osPreemptExtExit is called after returning from external code that
// may call ExitProcess.
//
// See osPreemptExtEnter for why this is nosplit.
//
//go:nosplit
func osPreemptExtExit(mp *m) {
	if !iswindows() {
		return
	}
	atomic.Store(&mp.preemptExtLock, 0)
}

//go:nosplit
func validSIGPROF(mp *m, c *sigctxt) bool {
	code := int32(c.sigcode())
	setitimer := code == _SI_KERNEL
	timer_create := code == _SI_TIMER

	if !(setitimer || timer_create) {
		return true
	}

	if mp == nil {
		return setitimer
	}

	if mp.profileTimerValid.Load() {
		return timer_create
	}

	return setitimer
}

func setProcessCPUProfiler(hz int32) {
	if iswindows() {
		// NT has no setitimer/SIGPROF: a profiler M samples threads
		// directly (os_cosmo_nt_prof.go, wave 3 item 3).
		ntSetProcessCPUProfiler(hz)
		return
	}
	setProcessCPUProfilerTimer(hz)
}

func setThreadCPUProfiler(hz int32) {
	if iswindows() {
		// Arm/disarm the NT profiling timer (per-thread timer_create
		// is not wired on cosmo; the timer is process-wide, upstream
		// windows' model).
		ntSetThreadCPUProfiler(hz)
	}
	mp := getg().m
	mp.profilehz = hz
}

const (
	_SI_USER  = 0
	_SI_TKILL = -6
)

//go:nosplit
func (c *sigctxt) sigFromUser() bool {
	code := int32(c.sigcode())
	return code == _SI_USER || code == _SI_TKILL
}

//go:nosplit
func (c *sigctxt) sigFromSeccomp() bool {
	return false
}

//go:nosplit
func mprotect(addr unsafe.Pointer, n uintptr, prot int32) (ret int32, errno int32) {
	r, _, err := cosmo.Syscall6(cosmo.SYS_MPROTECT, uintptr(addr), n, uintptr(prot), 0, 0, 0)
	return int32(r), int32(err)
}

// perThreadSyscallArgs contains the system call number, arguments, and
// expected return values for a system call to be executed on all threads.
type perThreadSyscallArgs struct {
	trap uintptr
	a1   uintptr
	a2   uintptr
	a3   uintptr
	a4   uintptr
	a5   uintptr
	a6   uintptr
	r1   uintptr
	r2   uintptr
}

var perThreadSyscall perThreadSyscallArgs

//go:nosplit
func runPerThreadSyscall() {
	gp := getg()
	if gp.m.needPerThreadSyscall.Load() == 0 {
		return
	}

	args := perThreadSyscall
	r1, r2, errno := cosmo.Syscall6(args.trap, args.a1, args.a2, args.a3, args.a4, args.a5, args.a6)
	if GOARCH == "ppc64" || GOARCH == "ppc64le" {
		r2 = 0
	}
	if errno != 0 || r1 != args.r1 || r2 != args.r2 {
		print("trap:", args.trap, ", a123456=[", args.a1, ",", args.a2, ",", args.a3, ",", args.a4, ",", args.a5, ",", args.a6, "]\n")
		print("results: got {r1=", r1, ",r2=", r2, ",errno=", errno, "}, want {r1=", args.r1, ",r2=", args.r2, ",errno=0}\n")
		fatal("AllThreadsSyscall6 results differ between threads; runtime corrupted")
	}

	gp.m.needPerThreadSyscall.Store(0)
}

// futex is implemented in assembly
//
//go:noescape
func futex(addr unsafe.Pointer, op int32, val uint32, ts, addr2 *timespec, val3 uint32) int32

// cosmoStacksAreSystemAllocated reports whether new OS threads get
// system-allocated stacks on this host. Only the macOS path (ARM64 via the
// APE loader's pthread_create) provides them; the Linux clone() path needs
// Go-allocated stacks. (The NT CreateThread path also pivots onto the
// Go-allocated g0 stack, so it keeps the Linux bookkeeping.)
func cosmoStacksAreSystemAllocated() bool {
	return GOARCH == "arm64" && isdarwin()
}

// cosmoHostIsWindows reports whether the cosmo binary is running on a
// Windows NT host. It exists (with a false stub for non-cosmo GOOSes in
// stubs_noncosmo.go) so GOOS-generic code - sysReserveAligned's
// no-partial-unreserve branch in mem.go - can key on the HOST at run
// time the way GOOS=windows keys at compile time.
//
//go:nosplit
func cosmoHostIsWindows() bool {
	return iswindows()
}

// Win32 memory constants for the NT branches in mem_cosmo.go (shared
// with arm64, where they are unreachable - iswindows is constant false
// there).
const (
	_NT_MEM_COMMIT   = 0x1000
	_NT_MEM_RESERVE  = 0x2000
	_NT_MEM_DECOMMIT = 0x4000
	_NT_MEM_RELEASE  = 0x8000

	_NT_PAGE_READWRITE = 0x04
)
