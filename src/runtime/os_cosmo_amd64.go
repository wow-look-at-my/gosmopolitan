// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import _ "unsafe" // for go:linkname

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
// the two loader-filled IAT slots; see os_cosmo_nt.go) and installs
// the syscall package's WindowsFns hook. osinit calls osArchInit
// BEFORE getCPUCount - required, since getCPUCount's NT leg calls
// GetSystemInfo through the table resolved here. On Linux hosts this
// remains a no-op; the darwin path uses raw XNU syscall numbers
// rather than an APE-loader Syslib, so there is nothing to resolve.
func osArchInit() {
	if iswindows() {
		ntResolve()
		ntSetSyscallFns()
	}
}

// cosmoDarwinNumCPU: no Syslib on amd64, so no sysctl access; report
// "unknown" and let getCPUCount fall back to 1.
func cosmoDarwinNumCPU() int32 { return 0 }

// cosmoDarwinKqueueSupported: the darwin netpoller needs Apple libc's
// kqueue/kevent, reached through the arm64 Syslib's dlsym. amd64 has no
// Syslib (and macOS-Intel execution is not implemented: clone/futex are
// ENOSYS there), so the poller is unsupported and netpollinit fails
// with a clear message instead of issuing Linux syscalls XNU would kill.
func cosmoDarwinKqueueSupported() bool { return false }

// cosmoDarwinKqueue and cosmoDarwinKevent are unreachable on amd64
// (netpollinit throws first); keep the failures honest anyway.
func cosmoDarwinKqueue() (int32, int32) {
	return -1, 38 // ENOSYS
}

func cosmoDarwinKevent(kq int32, ch *keventt, nch int32, ev *keventt, nev int32, ts *timespec) (int32, int32) {
	return -1, 38 // ENOSYS
}

// pipe2 is implemented in sys_cosmo_amd64.s.
func pipe2(flags int32) (r, w int32, errno int32)

// minitProcid: Linux hosts use the tid. (The macOS-Intel runtime
// bring-up is pending; gettid's darwin branch is a raw-XNU stub.)
// NT wave 1: gettid would issue a raw SYSCALL, and signal sends are
// dropped on NT anyway, so procid stays 0 (GetCurrentThreadId is a
// later wave).
//
//go:nosplit
func minitProcid() uint64 {
	if iswindows() {
		return 0
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
