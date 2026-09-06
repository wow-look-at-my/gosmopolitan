// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import "unsafe"

// Real signal-handler installation on macOS hosts (hostos == XNU).
//
// The shared runtime code speaks Linux: Linux signal numbers, Linux
// sigactiont and stackt layouts, Linux flag values, Linux 8-byte
// sigsets. Everything below translates at the boundary and calls Apple
// libc through the APE loader's Syslib.
//
// All of it can run in the narrowest runtime contexts: setsig,
// setsigstack and getsig during dieFromSignal on the signal stack, and
// clearSignalHandlers and msigrestore in a child between fork and
// exec. So everything is nosplit, takes no locks, allocates nothing,
// and calls only pre-resolved async-signal-safe libc entries.

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

// darwinSigaction implements sysSigaction on XNU hosts, over Syslib
// offset 272, a sysret-wrapped passthrough to Apple libc sigaction. It
// takes Apple's struct sigaction {handler, mask u32, flags i32} with
// APPLE numbers and flag values, and libc supplies its own trampoline,
// which invokes the handler under SA_SIGINFO and calls sigreturn. So
// sigtramp here just returns and needs no restorer. A signal with no
// Apple equivalent succeeds as a no-op, because it cannot be generated
// on an XNU host and initsig stays oblivious; reading one back reports
// SIG_DFL. Answers 0 on success, like rt_sigaction.
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
// pthread_sigmask: translate `how` (+1, because Apple's
// BLOCK/UNBLOCK/SETMASK are 1/2/3 against Linux's 0/1/2) and remap the
// sigset bits both ways, into a 4-byte Apple sigset. Syslib offset 96
// is the raw libc function, which answers a positive APPLE errno
// directly. Crashes on failure like the Linux asm path, so a bad mask
// can never be silently ignored.
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
// stack_t {sp, size, flags}, whose SS_DISABLE is 4 against Linux's 2.
// Syslib offset 296 is sysret-wrapped libc sigaltstack.
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
	if r := int64(cosmoLibcCall6(lib.sigaltstack, anewp, aoldp, 0, 0, 0, 0)); r < 0 {
		// Apple rejects a NEW stack carrying any flag but SS_DISABLE
		// (EINVAL 22), one smaller than MINSIGSTKSZ (ENOMEM 12), and
		// any change made while running on it (EPERM 1). Print what it
		// said and what it was given: the message alone names none of
		// the three.
		print("runtime: sigaltstack errno=", -r, " sp=", hex(anew.ss_sp),
			" size=", anew.ss_size, " flags=", anew.ss_flags, "\n")
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

// darwinSetitimer implements setitimer on XNU hosts. setitimer is not
// in the Syslib, so this calls Apple libc setitimer, resolved by dlsym
// at startup; that stub is a shallow syscall wrapper, so the direct
// cosmoLibcCall6 style applies and no asmcgocall is needed. Apple's
// tv_usec is a 32-bit suseconds_t where Linux arm64's is an int64, and
// _ITIMER_* coincides, so mode passes through. Failures are ignored
// like the Linux asm path: a dead timer surfaces as a zero-sample
// profile, which the runtimeprobe cpuprof check turns into a loud
// FAIL. nosplit, so the stack cannot move between taking the pointers
// and the call.
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
