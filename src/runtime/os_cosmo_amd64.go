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
}

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
	_XNU_sysctl = 0x2000000 | 202
	_XNU_kqueue = 0x2000000 | 362
	_XNU_kevent = 0x2000000 | 363
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
// This says nothing about macOS-Intel as a whole. Thread creation and
// parking are still ENOSYS there (see clone and futex below), so nothing
// reaches netpollinit yet; that blocker is stated once, where it lives,
// rather than mirrored into a second false claim here.
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

// minitProcid: Linux hosts use the tid. (The macOS-Intel runtime
// bring-up is pending; gettid's darwin branch is a raw-XNU stub.)
// NT (wave 2): GetCurrentThreadId, resolved at osArchInit - which
// runs before m0's minit and long before any other thread starts.
// Must agree with the SYS_GETTID emulation (os_cosmo_nt_sys.go).
// Signal sends are still dropped on NT; the thread id becomes
// load-bearing in the signals/preemption wave.
//
//go:nosplit
func minitProcid() uint64 {
	if iswindows() {
		return uint64(uint32(ntcall(ntGetCurrentThreadIdFn, 0, 0, 0, 0, 0, 0)))
	}
	return uint64(gettid())
}

// darwinSignalM: macOS-Intel execution is not implemented; keep the
// pre-dispatch behavior (tgkill's asm has its own darwin branch).
func darwinSignalM(mp *m, sig int) {
	tgkill(getpid(), int(mp.procid), sig)
}

// sigaltstack is implemented in sys_cosmo_amd64.s (its darwin branch
// is a raw-XNU stub; the Intel-mac runtime bring-up is pending).
//
//go:noescape
func sigaltstack(new, old *stackt)

// setitimer is implemented in sys_cosmo_amd64.s (its raw-XNU darwin
// branch is the pending Intel-mac bring-up path; arm64 dispatches to
// dlsym'd Apple libc setitimer instead - os_cosmo_arm64.go).
//
//go:noescape
func setitimer(mode int32, new, old *itimerval)

// darwinSigprocmask and darwinSigaction are unreachable on amd64: the
// GOARCH == "arm64" guards in sigprocmask/sysSigaction compile their
// call sites away, and the asm paths keep the amd64 behavior. The
// stubs exist so the shared code links.
//
//go:nosplit
func darwinSigprocmask(how int32, new, old *sigset) {
	throw("darwinSigprocmask: not implemented on amd64")
}

//go:nosplit
func darwinSigaction(sig uint32, new, old *sigactiont) int32 {
	return -1
}
