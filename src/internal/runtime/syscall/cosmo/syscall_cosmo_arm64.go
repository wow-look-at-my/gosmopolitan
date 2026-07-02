// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

// Darwin (macOS ARM64) syscall emulation.
//
// On macOS the APE loader hands the runtime a Syslib table of Apple libc
// functions; raw SVC syscalls are forbidden by XNU. The assembly fast path
// in asm_cosmo_arm64.s dispatches the hot syscalls straight to Syslib
// entries. Everything else lands here (via a tail jump with an identical
// signature) and is emulated with libc functions the runtime resolved
// through the Syslib's dlsym at startup (see runtime.osArchInit).
//
// Results follow the Linux syscall convention the callers expect:
// (r1, r2, errno) with a positive LINUX errno number. Apple errnos from
// libc are translated with the shared table in runtime/sys_cosmo_arm64.s.

// DarwinFns holds host libc function pointers resolved by the runtime at
// startup on macOS. A zero field means the symbol was unavailable; the
// emulation then fails with ENOSYS so the gap is visible instead of
// silently misbehaving.
type DarwinFns struct {
	// Resolved via Syslib dlsym(RTLD_DEFAULT, ...).
	Getpid  uintptr
	Getppid uintptr
	Getuid  uintptr
	Geteuid uintptr
	Getgid  uintptr
	Getegid uintptr
	Umask   uintptr

	// Taken directly from the Syslib table.
	PthreadSelf uintptr
}

var darwinFns DarwinFns

// SetDarwinFns installs the resolved function table. Called once from
// runtime.osArchInit before any user code runs.
func SetDarwinFns(f *DarwinFns) {
	darwinFns = *f
}

// Linux arm64 syscall numbers emulated only by the slow path. The shared
// fast-path numbers live in defs_cosmo_arm64.go.
const (
	sysUMASK   = 166
	sysGETPPID = 173
	sysGETUID  = 174
	sysGETEUID = 175
	sysGETGID  = 176
	sysGETEGID = 177
)

const (
	darwinENOSYS = 38 // Linux ENOSYS
)

// darwinLibcCall6 calls a C function pointer with up to six integer
// arguments following the Apple ARM64 ABI. Thin tail jump to
// runtime.cosmoLibcCall6 (see asm_cosmo_arm64.s in this package).
//
//go:noescape
func darwinLibcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) uintptr

// syscall6SlowDarwin emulates Linux syscalls that the assembly fast path
// does not handle, using dlsym-resolved Apple libc functions. It is the
// tail-jump target of Syscall6's darwin path, so it must keep exactly
// Syscall6's signature.
//
// nosplit so the whole Syscall6 path stays safe in a forked child (no
// stack growth); the linker verifies the nosplit stack bound.
//
//go:nosplit
func syscall6SlowDarwin(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr) {
	switch num {
	case SYS_GETPID:
		return darwinCallNoError(darwinFns.Getpid)
	case sysGETPPID:
		return darwinCallNoError(darwinFns.Getppid)
	case sysGETUID:
		return darwinCallNoError(darwinFns.Getuid)
	case sysGETEUID:
		return darwinCallNoError(darwinFns.Geteuid)
	case sysGETGID:
		return darwinCallNoError(darwinFns.Getgid)
	case sysGETEGID:
		return darwinCallNoError(darwinFns.Getegid)
	case SYS_GETTID:
		// No Linux-style tid exists; use pthread_self like the
		// runtime's gettid does, so the two agree.
		return darwinCallNoError(darwinFns.PthreadSelf)
	case sysUMASK:
		if darwinFns.Umask == 0 {
			return ^uintptr(0), 0, darwinENOSYS
		}
		// umask cannot fail; mode bits have identical values on
		// Linux and Apple.
		return darwinLibcCall6(darwinFns.Umask, a1, 0, 0, 0, 0, 0), 0, 0
	}
	// Not emulated. Return ENOSYS so the failure is visible rather than
	// pretending the call succeeded.
	return ^uintptr(0), 0, darwinENOSYS
}

// darwinCallNoError invokes a libc function that cannot fail (getpid and
// friends) and shapes the result for Syscall6.
//
//go:nosplit
func darwinCallNoError(fn uintptr) (r1, r2, errno uintptr) {
	if fn == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	return darwinLibcCall6(fn, 0, 0, 0, 0, 0, 0), 0, 0
}
