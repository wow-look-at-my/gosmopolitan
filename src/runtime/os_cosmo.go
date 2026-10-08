// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/atomic"
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// sigPerThreadSyscall is the same signal (SIGSETXID) used by glibc for per-thread syscalls.
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
		// NT: WaitOnAddress has the same compare-and-wait, spurious-wakeup-permitted semantics as futex.
		ntFutexsleep(addr, val, ns)
		return
	}
	if isdarwin() {
		darwinFutexsleep(addr, val, ns)
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

// darwinFutexsleep is FUTEX_WAIT built out of a timed sleep, for XNU hosts.
// XNU has no futex, and the primitives closest to one are not in this tree's
// syscall table. Their numbers would have to be guessed - and a wrong syscall
// number does not fail. It calls a different syscall. A real sleep IS
// available, so the wait polls the word with a backoff.
//
//go:nosplit
func darwinFutexsleep(addr *uint32, val uint32, ns int64) {
	const (
		minSleepUsec = 20
		maxSleepUsec = 5000
	)
	var deadline int64
	if ns >= 0 {
		deadline = nanotime() + ns
	}
	sleep := uint32(minSleepUsec)
	for atomic.Load(addr) == val {
		var left int64
		if ns >= 0 {
			left = deadline - nanotime()
		}
		d, expired := darwinFutexDelay(sleep, left, ns >= 0)
		if expired {
			return
		}
		usleep(d)
		if sleep < maxSleepUsec {
			sleep *= 2
		}
	}
}

// darwinFutexDelay decides one iteration of darwinFutexsleep's wait: how
// long to sleep in microseconds, and whether the deadline has already
// passed. leftNsec is the time remaining and is read only when timed.
//
//go:nosplit
func darwinFutexDelay(sleep uint32, leftNsec int64, timed bool) (usec uint32, expired bool) {
	if !timed {
		return sleep, false
	}
	if leftNsec <= 0 {
		return 0, true
	}
	// Never overshoot the caller's deadline, and never round a nonzero
	// remainder down to a no-op sleep that would spin the CPU.
	if int64(sleep)*1000 > leftNsec {
		d := uint32(leftNsec / 1000)
		if d == 0 {
			d = 1
		}
		return d, false
	}
	return sleep, false
}

// If any procs are sleeping on addr, wake up at most cnt.
//
//go:nosplit
func futexwakeup(addr *uint32, cnt uint32) {
	if iswindows() {
		// Every caller passes cnt==1, so WakeByAddressSingle suffices.
		ntFutexwakeup(addr)
		return
	}
	if isdarwin() {
		// Nothing to signal.
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
		// GetSystemInfo through the NT table resolved at osArchInit (which osinit runs before getCPUCount, deliberately).
		return ntNumCPU()
	}
	if isdarwin() {
		// sched_getaffinity is Linux-only; on macOS ask the host via sysctl (arm64
		// Syslib).
		if n := cosmoDarwinNumCPU(); n > 0 {
			return n
		}
		return 1
	}
	// Use a conservative default. On Cosmopolitan, the actual CPU count is determined at runtime based on the host OS.
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
		ntNewosproc(mp)
		return
	}
	stk := unsafe.Pointer(mp.g0.stack.hi)
	if false {
		print("newosproc stk=", stk, " m=", mp, " g=", mp.g0, " clone=", abi.FuncPCABI0(clone), " id=", mp.id, " ostk=", &mp, "\n")
	}

	// Disable signals during clone, so that the new thread starts with signals disabled. It will enable them in minit.
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
		// The NT boot stub fabricates an auxv, and libcosmo's WinMain, which starts a cgo program, passes none.
		physPageSize = 0x1000
		return
	}
	if isdarwin() {
		// Darwin hands the process no auxv, and /proc is a Linux path.
		probePageSize()
		publishDarwinAuxv()
		return
	}
	// Fall back to /proc/self/auxv.
	fd := open(&procAuxv[0], 0 /* O_RDONLY */, 0)
	if fd < 0 {
		// On some platforms, /proc might not be available.
		probePageSize()
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

// probePageSize measures the page size with mmap and mincore, for a host
// that publishes no AT_PAGESZ. It leaves physPageSize alone when mmap fails.
func probePageSize() {
	const size = 256 << 10
	p, err := mmap(nil, size, _PROT_READ|_PROT_WRITE, _MAP_ANON|_MAP_PRIVATE, -1, 0)
	if err != 0 {
		return
	}
	var n uintptr
	for n = 4 << 10; n < size; n <<= 1 {
		if mincore(unsafe.Pointer(uintptr(p)+n), 1, &addrspace_vec[0]) == 0 {
			physPageSize = n
			break
		}
	}
	if physPageSize == 0 {
		physPageSize = size
	}
	munmap(p, size)
}

// darwinauxvbuf backs the auxv published below. It is static because sysargs runs before the allocator exists.
var darwinauxvbuf = [2]uintptr{_AT_HWCAP, 0}

// publishDarwinAuxv gives a macOS host an auxv, which Darwin does not.
func publishDarwinAuxv() {
	auxv = darwinauxvbuf[:]
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
	// Before anything else: GOOS names the host on this port, and the entry stub has already recorded which that is.
	setGOOS()
	osArchInit()
	ntBoot("osArchInit done")
	// After osArchInit: the NT probe needs the resolved import table.
	setGOARCH()
	ntBoot("setGOARCH done")
	// A macOS host's AT_HWCAP needs fixing up before internal/cpu reads it in cpuinit.
	fixAuxv()
	numCPUStartup = getCPUCount()
	ntBoot("osinit done")
}

var urandom_dev = []byte("/dev/urandom\x00")

func readRandom(r []byte) int {
	if iswindows() {
		// ProcessPrng (or RtlGenRandom), resolved at osArchInit.
		return ntReadRandom(r)
	}
	fd := open(&urandom_dev[0], 0 /* O_RDONLY */, 0)
	n := read(fd, unsafe.Pointer(&r[0]), int32(len(r)))
	closefd(fd)
	return int(n)
}

func goenvs() {
	if iswindows() {
		// The boot block's envp is empty on NT; the real environment comes from GetEnvironmentStringsW (os_cosmo_nt.go).
		ntGoenvs()
		return
	}
	goenvs_unix()
}

// cosmoMstartm0 is the fork's mstartm0 hook (proc.go).
func cosmoMstartm0() {
	if iswindows() {
		ntInitConsoleCtrl()
	}
}

// Called to do synchronous initialization of Go code built with
// -buildmode=c-archive or -buildmode=c-shared.
//
//go:nosplit
//go:nowritebarrierrec
func libpreinit() {
	initsig(true)
}

// Called to initialize a new m (including the bootstrap m).
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
		// Duplicate this thread's handle into m.thread so ntPreemptM (async preemption) can address it.
		ntMinitThread()
	}
	// minitProcid is per-arch: on macOS hosts (arm64) procid must hold the FULL pthread_t for pthread_kill.
	getg().m.procid = minitProcid()
}

// Called from dropm and mexit to undo the effect of an minit.
//
//go:nosplit
func unminit() {
	unminitSignals()
	if iswindows() {
		// Close m.thread under threadLock: from here on ntPreemptM treats this M as unpreemptible instead.
		ntUnminitThread()
	}
	getg().m.procid = 0
}

//go:nowritebarrierrec
func mdestroy(mp *m) {
}

func sigreturn__sigaction()
func sigtramp()
func cgoSigtramp()

// sigaltstack is per-arch: on arm64 a Go host dispatcher (signal_cosmo_xnu.go) that translates Apple's stack_t on XNU hosts.

// setitimer is per-arch: on arm64 a Go host dispatcher (os_cosmo_arm64.go) that translates the itimerval layout on XNU hosts.

//go:noescape
func rtsigprocmask(how int32, new, old *sigset, size int32)

//go:nosplit
//go:nowritebarrierrec
func sigprocmask(how int32, new, old *sigset) {
	if isdarwin() {
		// Apple `how` values, sigset width and signal numbering all differ, and darwinSigprocmask translates all of them.
		darwinSigprocmask(how, new, old)
		return
	}
	if iswindows() {
		// NT has no kernel signal mask.
		ntSigprocmask(how, new, old)
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
	if goarch.Is386 == 1 || goarch.IsAmd64 == 1 {
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
// Apple sigaction translation layer (XNU hosts).
//
//go:nosplit
func sysSigaction(sig uint32, new, old *sigactiont) {
	var ret int32
	if iswindows() {
		// NT: there is no kernel-side sigaction.
		ret = ntSigaction(sig, new, old)
	} else if isdarwin() {
		// Both arches come here: arm64 through the Syslib, amd64 through raw __sigaction with its own trampoline.
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

//go:noescape
func rt_sigaction(sig uintptr, new, old *sigactiont, size uintptr) int32

//go:nosplit
func fixSigactionForCgo(new *sigactiont) {
	if goarch.Is386 == 1 && new != nil {
		new.sa_flags &^= _SA_RESTORER
		new.sa_restorer = 0
	}
}

func getpid() int
func tgkill(tgid, tid, sig int)

// signalM sends a signal to mp. sig is a LINUX signal number.
func signalM(mp *m, sig int) {
	if iswindows() {
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

// osPreemptExtEnter is called before entering external code that may call
// ExitProcess (NT hosts.
//
//go:nosplit
func osPreemptExtEnter(mp *m) {
	if !iswindows() {
		return
	}
	for !atomic.Cas(&mp.preemptExtLock, 0, 1) {
		// An asynchronous preemption is in progress.
		osyield()
	}
}

// osPreemptExtExit is called after returning from external code that
// may call ExitProcess.
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
		ntSetProcessCPUProfiler(hz)
		return
	}
	setProcessCPUProfilerTimer(hz)
}

func setThreadCPUProfiler(hz int32) {
	if iswindows() {
		// Arm/disarm the NT profiling timer.
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

// perThreadSyscallArgs contains the system call number, arguments.
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
	if goarch.IsPpc64 == 1 || goarch.IsPpc64le == 1 {
		r2 = 0
	}
	if errno != 0 || r1 != args.r1 || r2 != args.r2 {
		print("trap:", args.trap, ", a123456=[", args.a1, ",", args.a2, ",", args.a3, ",", args.a4, ",", args.a5, ",", args.a6, "]\n")
		print("results: got {r1=", r1, ",r2=", r2, ",errno=", errno, "}, want {r1=", args.r1, ",r2=", args.r2, ",errno=0}\n")
		fatal("AllThreadsSyscall6 results differ between threads; runtime corrupted")
	}

	gp.m.needPerThreadSyscall.Store(0)
}

// syscall_runtime_doAllThreadsSyscall executes a system call on every M.
// It is the linux port's driver over the cosmo syscall entry and rests
// on the same properties os_linux.go documents.
//
// On a darwin or NT host the call runs on the calling thread alone. XNU
// keeps credentials per process, so one call is the process-wide change
// the caller asked for. Neither host can deliver sigPerThreadSyscall to
// another thread - darwinSignalM drops the realtime range and NT has no
// cross-thread signal - so the wait below would. Never end there.
//
//go:linkname syscall_runtime_doAllThreadsSyscall syscall.runtime_doAllThreadsSyscall
//go:uintptrescapes
func syscall_runtime_doAllThreadsSyscall(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr) {
	if isdarwin() || iswindows() {
		return cosmo.Syscall6(trap, a1, a2, a3, a4, a5, a6)
	}

	// STW so user goroutines see an atomic change to thread state.
	stw := stopTheWorld(stwAllThreadsSyscall)

	// allocmLock prevents new Ms while this runs, and serializes callers.
	allocmLock.lock()
	acquirem()

	r1, r2, errno := cosmo.Syscall6(trap, a1, a2, a3, a4, a5, a6)
	if errno != 0 {
		releasem(getg().m)
		allocmLock.unlock()
		startTheWorld(stw)
		return r1, r2, errno
	}

	perThreadSyscall = perThreadSyscallArgs{
		trap: trap,
		a1:   a1,
		a2:   a2,
		a3:   a3,
		a4:   a4,
		a5:   a5,
		a6:   a6,
		r1:   r1,
		r2:   r2,
	}

	// Wait for every thread to set procid before any signal goes out.
	for mp := allm; mp != nil; mp = mp.alllink {
		for atomic.Load64(&mp.procid) == 0 {
			osyield()
		}
	}

	gp := getg()
	tid := gp.m.procid
	for mp := allm; mp != nil; mp = mp.alllink {
		if atomic.Load64(&mp.procid) == tid {
			continue
		}
		mp.needPerThreadSyscall.Store(1)
		signalM(mp, sigPerThreadSyscall)
	}

	for mp := allm; mp != nil; mp = mp.alllink {
		if mp.procid == tid {
			continue
		}
		for mp.needPerThreadSyscall.Load() != 0 {
			osyield()
		}
	}

	perThreadSyscall = perThreadSyscallArgs{}

	releasem(getg().m)
	allocmLock.unlock()
	startTheWorld(stw)

	return r1, r2, errno
}

//go:noescape
func futex(addr unsafe.Pointer, op int32, val uint32, ts, addr2 *timespec, val3 uint32) int32

// cosmoStacksAreSystemAllocated reports whether new OS threads get
// system-allocated stacks on this host.
func cosmoStacksAreSystemAllocated() bool {
	return goarch.IsArm64 == 1 && isdarwin()
}

// cosmoHostIsWindows reports whether the cosmo binary is running on a Windows
// NT host.
//
//go:nosplit
func cosmoHostIsWindows() bool {
	return iswindows()
}

// Win32 memory constants for the NT branches in mem_cosmo.go.
const (
	_NT_MEM_COMMIT   = 0x1000
	_NT_MEM_RESERVE  = 0x2000
	_NT_MEM_DECOMMIT = 0x4000
	_NT_MEM_RELEASE  = 0x8000

	_NT_PAGE_READWRITE = 0x04
)
