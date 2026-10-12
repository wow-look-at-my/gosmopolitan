// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

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

// osArchInit resolves the NT function table on Windows hosts (from both
// loader-filled IAT slots.
func osArchInit() {
	if iswindows() {
		ntResolve()
		ntBoot("ntResolve done")
		ntSetSyscallFns()
		ntBoot("syscall fns set")
		ntBootInit()
		ntBoot("ntBootInit done")
	}
	if isdarwin() {
		// The kernel has to be told where to enter a new thread before the first
		// bsdthread_create.
		if errno := cosmoBsdthreadRegister(); errno != 0 {
			print("runtime: bsdthread_register failed with errno ", errno, "\n")
			throw("cosmo: bsdthread_register")
		}
	}
}

// cosmoBsdthreadRegister is in sys_cosmo_amd64.s.
func cosmoBsdthreadRegister() int32

//go:noescape
func xnuUlockWait(op uint32, addr *uint32, value uint64, timeout uint32) int32

//go:noescape
func xnuUlockWake(op uint32, addr *uint32, wake uint64) int32

// cosmoBsdthreadStart is in sys_cosmo_amd64.s.
func cosmoBsdthreadStart()

//go:linkname cosmo_xlat_errno_ax
func cosmo_xlat_errno_ax()

//go:linkname cosmo_xlat_oflags_dx
func cosmo_xlat_oflags_dx()

// cosmoXlatErrno is the Go-callable form of cosmo_xlat_errno_ax (sys_cosmo_amd64.s), so a test can pin the table.
func cosmoXlatErrno(e uint32) uint32

// cosmoDarwinNumCPU reads hw.ncpu through raw XNU __sysctl. amd64 has no
// Syslib and so cannot call sysctlbyname the way arm64 does.
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

// cosmoDarwinSysctlCall issues Apple's sysctl with a numeric MIB, the
// same raw __sysctl both readers above use. amd64 has no Syslib, so
// there is nothing to dlsym and the syscall is the only route.
func cosmoDarwinSysctlCall(mib *uint32, miblen uint32, old unsafe.Pointer, oldlen *uintptr, newp unsafe.Pointer, newlen uintptr) int32 {
	_, e := cosmoXnuSyscall6(_XNU_sysctl,
		uintptr(unsafe.Pointer(mib)), uintptr(miblen),
		uintptr(old), uintptr(unsafe.Pointer(oldlen)),
		uintptr(newp), newlen)
	if e != 0 {
		return -1
	}
	return 0
}

// cosmoDarwinHostname reads kern.hostname through the same raw __sysctl,
// with the numeric MIB. That is where macOS keeps the machine's name and
// where a native darwin build's os.Hostname reads it. Answers "" when
// the call fails, which the caller reports rather than papers over.
func cosmoDarwinHostname() string {
	mib := [2]uint32{_CTL_KERN, _KERN_HOSTNAME}
	var buf [512]byte
	nout := uintptr(len(buf))
	_, e := cosmoXnuSyscall6(_XNU_sysctl,
		uintptr(unsafe.Pointer(&mib[0])), 2,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&nout)),
		0, 0)
	if e != 0 || nout == 0 || nout > uintptr(len(buf)) {
		return ""
	}
	n := int(nout)
	// sysctl counts the NUL it wrote; the string must not.
	for n > 0 && buf[n-1] == 0 {
		n--
	}
	return string(buf[:n])
}

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

	_CTL_KERN      = 1
	_KERN_HOSTNAME = 10
)

//go:noescape
func cosmoXnuSyscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1 uintptr, errno int32)

// cosmoDarwinKqueueSupported: amd64 has no Syslib, so it cannot reach Apple libc kqueue the way arm64 does.
func cosmoDarwinKqueueSupported() bool { return true }

// cosmoDarwinKqueue creates a kqueue.
func cosmoDarwinKqueue() (int32, int32) {
	r, e := cosmoXnuSyscall6(_XNU_kqueue, 0, 0, 0, 0, 0, 0)
	if e != 0 {
		return -1, e
	}
	return int32(r), 0
}

// cosmoDarwinKevent registers changes and collects events. A nil ts
// means "block indefinitely", which XNU spells the same way Apple libc
// does: a null pointer.
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

// minitProcid: Linux hosts use the tid. macOS hosts use the thread's mach
// port, which __pthread_kill (tgkill's darwin branch) addresses.
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

// cosmoMachThreadSelf is in sys_cosmo_amd64.s: the thread_self_trap mach trap, returning this thread's port name.
func cosmoMachThreadSelf() uint32

// darwinSignalM sends sig (a LINUX signal number) to mp's thread. tgkill's darwin branch translates the number and issues __pthread_kill.
func darwinSignalM(mp *m, sig int) {
	tgkill(getpid(), int(mp.procid), sig)
}

// sigaltstack is a Go host dispatcher (signal_cosmo_xnu_amd64.go).

//go:noescape
func setitimer(mode int32, new, old *itimerval)
