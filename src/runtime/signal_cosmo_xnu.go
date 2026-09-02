// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import "unsafe"

// Real signal-handler installation on macOS hosts (hostos == XNU).
//
// The shared runtime code (setsig, sigprocmask, signalstack, ...)
// speaks Linux: Linux signal numbers, Linux sigactiont/stackt layouts,
// Linux flag values, Linux 8-byte sigsets. On XNU everything below
// translates at the boundary and calls Apple libc through the APE
// loader's Syslib:
//
//   - sigaction (Syslib offset 272) is a sysret-wrapped passthrough to
//     Apple libc sigaction (verified in ape-m1.c sys_sigaction): it
//     takes Apple's libc struct sigaction {handler, mask u32, flags
//     i32} - upstream defs_darwin_arm64.go's usigactiont - with APPLE
//     signal numbers and APPLE flag values, and libc supplies its own
//     signal trampoline, which invokes our handler as handler(sig,
//     info, ctx) with SA_SIGINFO and calls sigreturn when the handler
//     returns. The runtime's sigtramp therefore just returns (like
//     upstream darwin) - no restorer is needed.
//   - pthread_sigmask (offset 96) is the raw libc function: it returns
//     a positive APPLE errno directly and takes Apple `how` values
//     (SIG_BLOCK/UNBLOCK/SETMASK = 1/2/3 where Linux uses 0/1/2 - the
//     same off-by-one wave 4 fixed on the amd64 side) and a 4-byte
//     Apple sigset.
//   - sigaltstack (offset 296) is sysret-wrapped libc sigaltstack and
//     takes Apple's stack_t {sp, size, flags} - Linux arm64 orders it
//     {sp, flags, pad, size} - with SS_DISABLE = 4 (Linux: 2).
//   - setitimer is not in the Syslib at all; darwinSetitimer calls
//     raw Apple libc setitimer resolved via dlsym at startup
//     (cosmoDarwinSetitimerFn, os_cosmo_arm64.go) and translates the
//     itimerval layout (Apple's tv_usec is a 32-bit suseconds_t;
//     Linux arm64's is int64).
//
// All functions here can run in the narrowest runtime contexts:
// setsig/setsigstack/getsig during dieFromSignal on the signal stack,
// clearSignalHandlers and msigrestore in the child between fork and
// exec. Everything is nosplit, takes no locks, allocates nothing, and
// calls only the pre-resolved async-signal-safe libc entries.

// The Apple sigaction flag values and both translation directions live
// in signal_cosmo_xnu_flags.go, which carries no architecture tag: amd64
// reaches Apple sigaction through the raw __sigaction syscall rather
// than the Syslib, and the flags are identical either way.

// Apple sigaltstack flag values. SS_ONSTACK is 1 on both systems;
// SS_DISABLE is 4 on Apple, 2 on Linux (_SS_DISABLE, os2_cosmo.go).
const (
	xnuSS_ONSTACK = 0x1
	xnuSS_DISABLE = 0x4
)

// xnuSigactiont is Apple's libc struct sigaction (what sigaction(2)'s
// libc wrapper takes; upstream runtime calls it usigactiont). The
// kernel-side trampoline field does not appear here - libc adds it.
type xnuSigactiont struct {
	sa_handler uintptr
	sa_mask    uint32
	sa_flags   int32
}

// darwinSigaction implements sysSigaction on XNU hosts: translate the
// Linux sigactiont both ways and call Apple sigaction via the Syslib.
// Returns 0 on success, nonzero on failure (like rt_sigaction).
//
// Signals with no Apple equivalent (SIGSTKFLT, SIGPWR, the realtime
// range 32..64) succeed as a no-op: they cannot be generated on an
// XNU host, and initsig/setsigstack/clearSignalHandlers must remain
// oblivious. Reading one back reports the zero sigactiont (SIG_DFL).
//
//go:nosplit
//go:nowritebarrierrec
func darwinSigaction(sig uint32, new, old *sigactiont) int32 {
	asig := cosmoSigL2A(sig)
	if asig == 0 {
		if old != nil {
			*old = sigactiont{}
		}
		return 0
	}
	lib := __syslib
	if lib == nil || lib.sigaction == 0 {
		return -1
	}
	var anew, aold xnuSigactiont
	var anewp, aoldp uintptr
	if new != nil {
		// SIG_DFL (0) and SIG_IGN (1) coincide on both systems; real
		// handler pointers (our sigtramp) pass through untranslated.
		anew.sa_handler = new.sa_handler
		anew.sa_flags = xnuSigFlagsL2A(new.sa_flags)
		anew.sa_mask = cosmoSigmaskL2A(new.sa_mask)
		anewp = uintptr(unsafe.Pointer(&anew))
	}
	if old != nil {
		aoldp = uintptr(unsafe.Pointer(&aold))
	}
	// Sysret-wrapped: 0 or -errno (Apple numbering).
	if int64(cosmoLibcCall6(lib.sigaction, uintptr(asig), anewp, aoldp, 0, 0, 0)) < 0 {
		return -1
	}
	if old != nil {
		old.sa_handler = aold.sa_handler
		old.sa_flags = xnuSigFlagsA2L(aold.sa_flags)
		old.sa_restorer = 0
		old.sa_mask = cosmoSigmaskA2L(aold.sa_mask)
	}
	return 0
}

// darwinSigprocmask implements sigprocmask on XNU hosts with
// pthread_sigmask: translate `how` (+1) and remap the sigset bits in
// both directions. Crashes on failure like the Linux asm path, so a
// bad mask can never be silently ignored.
//
//go:nosplit
//go:nowritebarrierrec
func darwinSigprocmask(how int32, new, old *sigset) {
	lib := __syslib
	if lib == nil || lib.pthread_sigmask == 0 {
		throw("darwinSigprocmask: no pthread_sigmask")
	}
	var anew, aold uint32
	var anewp, aoldp uintptr
	if new != nil {
		anew = cosmoSigmaskL2A(uint64(new[0]) | uint64(new[1])<<32)
		anewp = uintptr(unsafe.Pointer(&anew))
	}
	if old != nil {
		aoldp = uintptr(unsafe.Pointer(&aold))
	}
	// pthread_sigmask returns the error directly (no errno, no sysret).
	if cosmoLibcCall6(lib.pthread_sigmask, uintptr(how+1), anewp, aoldp, 0, 0, 0) != 0 {
		throw("darwinSigprocmask: pthread_sigmask failed")
	}
	if old != nil {
		m := cosmoSigmaskA2L(aold)
		old[0] = uint32(m)
		old[1] = uint32(m >> 32)
	}
}

// darwinSigaltstack implements sigaltstack on XNU hosts, translating
// the Linux arm64 stackt {sp, flags, pad, size} to and from Apple's
// stack_t {sp, size, flags}.
//
//go:nosplit
//go:nowritebarrierrec
func darwinSigaltstack(new, old *stackt) {
	lib := __syslib
	if lib == nil || lib.sigaltstack == 0 {
		throw("darwinSigaltstack: no sigaltstack")
	}
	var anew, aold xnuStackt
	var anewp, aoldp uintptr
	if new != nil {
		anew.ss_sp = *(*uintptr)(unsafe.Pointer(&new.ss_sp))
		anew.ss_size = new.ss_size
		var fl int32
		if new.ss_flags&_SS_DISABLE != 0 {
			fl |= xnuSS_DISABLE
		}
		if new.ss_flags&xnuSS_ONSTACK != 0 { // SS_ONSTACK is 1 on both
			fl |= xnuSS_ONSTACK
		}
		anew.ss_flags = fl
		anewp = uintptr(unsafe.Pointer(&anew))
	}
	if old != nil {
		aoldp = uintptr(unsafe.Pointer(&aold))
	}
	// Sysret-wrapped: 0 or -errno (Apple numbering).
	if int64(cosmoLibcCall6(lib.sigaltstack, anewp, aoldp, 0, 0, 0, 0)) < 0 {
		throw("darwinSigaltstack: sigaltstack failed")
	}
	if old != nil {
		// Uintptr store: stackt.ss_sp is *byte, but this can run in
		// nowritebarrierrec contexts (same trick as setSignalstackSP).
		*(*uintptr)(unsafe.Pointer(&old.ss_sp)) = aold.ss_sp
		old.ss_size = aold.ss_size
		var fl int32
		if aold.ss_flags&xnuSS_DISABLE != 0 {
			fl |= _SS_DISABLE
		}
		if aold.ss_flags&xnuSS_ONSTACK != 0 {
			fl |= xnuSS_ONSTACK
		}
		old.ss_flags = fl
	}
}

// sigaltstack dispatches on the host: Apple's stack_t layout and flag
// values differ from Linux's, so the raw syscall path only serves
// Linux hosts.
//
//go:nosplit
//go:nowritebarrierrec
func sigaltstack(new, old *stackt) {
	if isdarwin() {
		darwinSigaltstack(new, old)
		return
	}
	sigaltstackLinux(new, old)
}

// sigaltstackLinux is the raw Linux sigaltstack syscall
// (sys_cosmo_arm64.s).
//
//go:noescape
func sigaltstackLinux(new, old *stackt)

// darwinSetitimer implements setitimer on XNU hosts: translate the
// Linux itimerval both ways and call Apple libc setitimer, resolved
// via dlsym at startup (the libc stub is a shallow syscall wrapper,
// so the direct cosmoLibcCall6 style used for kqueue/kevent applies -
// no asmcgocall needed). The _ITIMER_* mode values coincide on both
// systems, so mode passes through. Failures are ignored like the
// Linux asm path: a dead timer surfaces as a zero-sample profile,
// which the runtimeprobe cpuprof check turns into a loud FAIL.
//
// nosplit so the stack cannot move between taking anewp/aoldp and the
// call (the darwinSigaction pattern).
//
//go:nosplit
func darwinSetitimer(mode int32, new, old *itimerval) {
	if cosmoDarwinSetitimerFn == 0 {
		return
	}
	var anew, aold xnuItimerval
	var anewp, aoldp uintptr
	if new != nil {
		anew.it_interval = cosmoTimevalL2X(&new.it_interval)
		anew.it_value = cosmoTimevalL2X(&new.it_value)
		anewp = uintptr(unsafe.Pointer(&anew))
	}
	if old != nil {
		aoldp = uintptr(unsafe.Pointer(&aold))
	}
	cosmoLibcCall6(cosmoDarwinSetitimerFn, uintptr(mode), anewp, aoldp, 0, 0, 0)
	if old != nil {
		old.it_interval = cosmoTimevalX2L(&aold.it_interval)
		old.it_value = cosmoTimevalX2L(&aold.it_value)
	}
}
