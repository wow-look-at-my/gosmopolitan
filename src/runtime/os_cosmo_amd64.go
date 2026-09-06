// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import "unsafe"

// Host OS constants (passed in CL by APE loader on x86_64)
const (
	_HOSTLINUX   = 0
	_HOSTMETAL   = 1
	_HOSTWINDOWS = 2
	_HOSTXNU     = 8
	_HOSTFREEBSD = 9
	_HOSTOPENBSD = 10
	_HOSTNETBSD  = 11
)

// __hostos indicates the host operating system.
// Set by rt0_cosmo_amd64.s at startup.
// 0=Linux, 8=XNU/macOS, 9=FreeBSD, etc.
//
//go:linkname __hostos
var __hostos int32

// isdarwin returns true if running on macOS
//
//go:nosplit
func isdarwin() bool {
	return __hostos == _HOSTXNU
}

// iswindows returns true if running on Windows NT.
//
//go:nosplit
func iswindows() bool {
	return __hostos == _HOSTWINDOWS
}

// osArchInit resolves the NT function table on Windows hosts (from
// the two loader-filled IAT slots; see os_cosmo_nt.go), installs the
// syscall package's WindowsFns hook, and runs the NT boot
// initialization (std-fd seeding, console UTF-8/VT setup, AT_RANDOM
// upgrade; see ntBootInit in os_cosmo_nt_sys.go). osinit calls
// osArchInit BEFORE getCPUCount - required, since getCPUCount's NT
// leg calls GetSystemInfo through the table resolved here. On Linux
// hosts this remains a no-op; the darwin path uses raw XNU syscall
// numbers rather than an APE-loader Syslib, so there is nothing to
// resolve.
func osArchInit() {
	if iswindows() {
		ntResolve()
		ntSetSyscallFns()
		ntBootInit()
	}
	if isdarwin() {
		// The kernel has to be told where to enter a new thread before
		// the first bsdthread_create, and there is nowhere later to do
		// it: newosproc runs on whatever M needs the thread. Failing
		// here is right - an unregistered process cannot create a
		// thread at all, so the alternative is dying at the first
		// newosproc with a less specific message.
		if errno := cosmoBsdthreadRegister(); errno != 0 {
			print("runtime: bsdthread_register failed with errno ", errno, "\n")
			throw("cosmo: bsdthread_register")
		}
	}
}

// cosmoBsdthreadRegister is in sys_cosmo_amd64.s. It registers
// cosmoBsdthreadStart as the entry point for threads made by
// bsdthread_create, which is how darwin/amd64 serves clone.
func cosmoBsdthreadRegister() int32

// cosmoBsdthreadStart is in sys_cosmo_amd64.s. Nothing in Go calls it -
// the KERNEL enters it, with a register state of its own choosing, when a
// bsdthread_create thread starts. The declaration exists because asmdecl
// requires every asm TEXT in the package to have one; taking its address
// is all the Go side ever does.
func cosmoBsdthreadStart()

// cosmo_xlat_errno_ax is a register-convention helper in sys_cosmo_amd64.s:
// it translates an Apple errno in AX to its Linux value. It takes no Go
// arguments and is not callable from Go; internal/runtime/syscall/cosmo
// reaches it from its own assembly. The declaration exists only so the
// symbol carries a linkname push, which is what cmd/link's cross-package
// reference check looks for. arm64's counterpart is cosmo_xlat_errno_r0
// (os_cosmo_arm64.go); both read the one table in sys_cosmo_errno.s.
//
//go:linkname cosmo_xlat_errno_ax
func cosmo_xlat_errno_ax()

// cosmo_xlat_oflags_dx is the same shape: a register-convention helper in
// sys_cosmo_amd64.s that turns Linux open(2) flags in DX into Apple ones.
// internal/runtime/syscall/cosmo's openat reaches it from assembly, so the
// symbol needs the linkname push. arm64's counterpart is
// cosmo_xlat_oflags_r2 (os_cosmo_arm64.go).
//
//go:linkname cosmo_xlat_oflags_dx
func cosmo_xlat_oflags_dx()

// cosmoXlatErrno is the Go-callable form of cosmo_xlat_errno_ax
// (sys_cosmo_amd64.s), so a test can pin the table.
func cosmoXlatErrno(e uint32) uint32

// cosmoDarwinNumCPU reads hw.ncpu through raw XNU __sysctl. amd64 has no
// Syslib and so cannot call sysctlbyname the way arm64 does, but the
// numeric MIB needs no name lookup: the syscall number and both MIB
// constants come from this tree (syscall/zsysnum_darwin_amd64.go and
// os_darwin.go's own getCPUCount). Returns 0 when the call fails, which
// is what getCPUCount treats as "unknown".
func cosmoDarwinNumCPU() int32 {
	mib := [2]uint32{_CTL_HW, _HW_NCPU}
	out := uint32(0)
	nout := unsafe.Sizeof(out)
	_, e := cosmoXnuSyscall6(_XNU_sysctl,
		uintptr(unsafe.Pointer(&mib[0])), 2,
		uintptr(unsafe.Pointer(&out)),
		uintptr(unsafe.Pointer(&nout)),
		0, 0)
	if e != 0 {
		return 0
	}
	return int32(out)
}

// XNU BSD numbers for the netpoller's two syscalls, from the tree's own
// authority (syscall/zsysnum_darwin_amd64.go), with the SYSCALL_CLASS_UNIX
// prefix. kevent 363 is the legacy entry, whose struct kevent is the
// 64-bit layout keventt already describes (netpoll_cosmo_xnu.go) - the
// same one arm64 passes to Apple libc.
const (
	_XNU_sigaction   = 0x2000000 | 46
	_XNU_sigprocmask = 0x2000000 | 48
	_XNU_sigaltstack = 0x2000000 | 53
	_XNU_sysctl      = 0x2000000 | 202
	_XNU_kqueue      = 0x2000000 | 362
	_XNU_kevent      = 0x2000000 | 363
)

// The hw.ncpu MIB, spelled the way os_darwin.go spells it. That file is
// GOOS=darwin only, so cosmo cannot share the declaration.
const (
	_CTL_HW  = 6
	_HW_NCPU = 3
)

// cosmoXnuSyscall6 is in sys_cosmo_amd64.s. It answers ENOSYS on any
// host that is not XNU.
//
//go:noescape
func cosmoXnuSyscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1 uintptr, errno int32)

// cosmoDarwinKqueueSupported: amd64 has no Syslib, so it cannot reach
// Apple libc kqueue the way arm64 does - but it does not need to. The
// amd64 darwin path issues raw XNU syscalls, and both numbers are known,
// so the poller is served directly.
//
// This says nothing about whether macOS-Intel has ever been RUN. Its
// syscall surface is complete now - thread creation, signal
// installation and the signal mask included - but there is no
// Intel-mac runner, so none of it has executed.
func cosmoDarwinKqueueSupported() bool { return true }

// cosmoDarwinKqueue creates a kqueue. Returns the descriptor, or
// (-1, errno) with a LINUX errno number.
func cosmoDarwinKqueue() (int32, int32) {
	r, e := cosmoXnuSyscall6(_XNU_kqueue, 0, 0, 0, 0, 0, 0)
	if e != 0 {
		return -1, e
	}
	return int32(r), 0
}

// cosmoDarwinKevent registers changes and collects events. Returns the
// number of events placed in ev, or (-1, errno) with a LINUX errno
// number. A nil ts means "block indefinitely", which XNU spells the same
// way Apple libc does: a null pointer.
func cosmoDarwinKevent(kq int32, ch *keventt, nch int32, ev *keventt, nev int32, ts *timespec) (int32, int32) {
	r, e := cosmoXnuSyscall6(_XNU_kevent,
		uintptr(uint32(kq)),
		uintptr(unsafe.Pointer(ch)),
		uintptr(uint32(nch)),
		uintptr(unsafe.Pointer(ev)),
		uintptr(uint32(nev)),
		uintptr(unsafe.Pointer(ts)))
	if e != 0 {
		return -1, e
	}
	return int32(r), 0
}

// pipe2 is implemented in sys_cosmo_amd64.s.
func pipe2(flags int32) (r, w int32, errno int32)

// minitProcid: Linux hosts use the tid. macOS hosts use the thread's
// mach port, which __pthread_kill (tgkill's darwin branch) addresses:
// cosmoBsdthreadStart stores the port the kernel hands a new thread
// before minit runs, and m0, which no bsdthread_create made, asks
// thread_self_trap. NT (wave 2): GetCurrentThreadId, resolved at
// osArchInit - which runs before m0's minit and long before any other
// thread starts. Must agree with the SYS_GETTID emulation
// (os_cosmo_nt_sys.go).
//
//go:nosplit
func minitProcid() uint64 {
	if iswindows() {
		return uint64(uint32(ntcall(ntGetCurrentThreadIdFn, 0, 0, 0, 0, 0, 0)))
	}
	if isdarwin() {
		if port := getg().m.procid; port != 0 {
			return port
		}
		return uint64(cosmoMachThreadSelf())
	}
	return uint64(gettid())
}

// cosmoMachThreadSelf is in sys_cosmo_amd64.s: the thread_self_trap mach
// trap, returning this thread's port name. XNU hosts only.
func cosmoMachThreadSelf() uint32

// darwinSignalM sends sig (a LINUX signal number) to mp's thread. tgkill's
// darwin branch translates the number and issues __pthread_kill on the
// mach port in m.procid; a signal with no Apple number is dropped there.
func darwinSignalM(mp *m, sig int) {
	tgkill(getpid(), int(mp.procid), sig)
}

// sigaltstack is a Go host dispatcher (signal_cosmo_xnu_amd64.go): it
// translates Apple's stack_t on XNU hosts and issues the raw Linux
// syscall (sigaltstackLinux, sys_cosmo_amd64.s) elsewhere.

// setitimer is implemented in sys_cosmo_amd64.s (its raw-XNU darwin
// branch is the pending Intel-mac bring-up path; arm64 dispatches to
// dlsym'd Apple libc setitimer instead - os_cosmo_arm64.go).
//
//go:noescape
func setitimer(mode int32, new, old *itimerval)
