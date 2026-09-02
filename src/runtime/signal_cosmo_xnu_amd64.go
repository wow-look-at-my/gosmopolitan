// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import (
	"internal/abi"
	"unsafe"
)

// Real signal-handler installation on macOS-Intel hosts.
//
// arm64 reaches Apple's LIBC sigaction through the APE loader's Syslib
// (signal_cosmo_xnu.go). amd64 has no Syslib, so it issues the raw
// __sigaction syscall - which is a different interface, not the same one
// by another route:
//
//   - The kernel struct carries an sa_tramp field that the libc struct
//     does not. libc fills it with its own trampoline; a raw caller must
//     supply one, and cosmoXnuSigtramp is it.
//   - The kernel enters that trampoline with the handler, an infostyle
//     token, and the signal arguments, and the trampoline is responsible
//     for calling sigreturn when the handler comes back. libc does that
//     part too, which is why the arm64 path can ignore it.
//
// The flag and mask translations are shared with arm64
// (signal_cosmo_xnu_flags.go, sigxlat_cosmo.go): only the call differs.
//
// The ABI is Go's own pre-1.12 darwin port (go1.8
// runtime/sys_darwin_amd64.s, defs_darwin_amd64.go), the same source the
// bsdthread thread-creation path came from.

// xnuKsigactiont is Apple's KERNEL struct sigaction, what __sigaction
// takes. Upstream's darwin port calls it sigactiont; the libc-facing one
// without sa_tramp is xnuSigactiont on the arm64 side.
type xnuKsigactiont struct {
	sa_handler uintptr
	sa_tramp   uintptr
	sa_mask    uint32
	sa_flags   int32
}

// xnuSigactiont is Apple's user64_sigaction, the OLD action __sigaction
// copies out (XNU kern_sig.c sigaction_kern_to_user64). It has no
// sa_tramp: the kernel keeps the trampoline and never reports it back.
type xnuSigactiont struct {
	sa_handler uintptr
	sa_mask    uint32
	sa_flags   int32
}

// Apple sigaltstack flag values. SS_ONSTACK is 1 on both systems;
// SS_DISABLE is 4 on Apple, 2 on Linux (_SS_DISABLE, os2_cosmo.go).
const (
	xnuSS_ONSTACK = 0x1
	xnuSS_DISABLE = 0x4
)

// cosmoXnuSigtramp is in sys_cosmo_amd64.s. The KERNEL enters it - it is
// never called from Go - so the declaration exists for asmdecl and for
// taking its address.
func cosmoXnuSigtramp()

// darwinSigaction implements sysSigaction on macOS-Intel: translate the
// Linux sigactiont both ways and issue __sigaction. Returns 0 on
// success, nonzero on failure, like rt_sigaction.
//
// Signals with no Apple equivalent (SIGSTKFLT, SIGPWR, the realtime
// range) succeed as a no-op and read back as SIG_DFL, matching arm64:
// they cannot be raised on an XNU host, and initsig and
// clearSignalHandlers must stay oblivious.
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
	var anew xnuKsigactiont
	var aold xnuSigactiont
	var anewp, aoldp uintptr
	if new != nil {
		// SIG_DFL (0) and SIG_IGN (1) coincide on both systems; a real
		// handler pointer passes through untranslated.
		anew.sa_handler = new.sa_handler
		anew.sa_flags = xnuSigFlagsL2A(new.sa_flags)
		anew.sa_mask = cosmoSigmaskL2A(new.sa_mask)
		// Only a real handler needs a trampoline. Giving one to SIG_DFL
		// or SIG_IGN would hand the kernel a return path for a delivery
		// it is never going to make.
		if anew.sa_handler > 1 {
			anew.sa_tramp = abi.FuncPCABI0(cosmoXnuSigtramp)
		}
		anewp = uintptr(unsafe.Pointer(&anew))
	}
	if old != nil {
		aoldp = uintptr(unsafe.Pointer(&aold))
	}
	if _, errno := cosmoXnuSyscall6(_XNU_sigaction, uintptr(asig), anewp, aoldp, 0, 0, 0); errno != 0 {
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

// darwinSigprocmask implements sigprocmask on macOS-Intel: translate
// `how` (Linux 0/1/2 to Apple 1/2/3) and remap the sigset bits in both
// directions, then issue the raw sigprocmask syscall. arm64 reaches
// Apple libc pthread_sigmask instead (signal_cosmo_xnu.go); the
// translations are the same.
//
// A Linux sigset is 8 bytes and an Apple one is 4, and the two number
// their signals differently. The asm path this replaces
// (rtsigprocmask's darwin branch) handed the kernel the Linux mask
// untouched, so every mask it set named the wrong signals.
//
// Crashes on failure like the Linux asm path, so a bad mask can never
// be silently ignored.
//
//go:nosplit
//go:nowritebarrierrec
func darwinSigprocmask(how int32, new, old *sigset) {
	var anew, aold uint32
	var anewp, aoldp uintptr
	if new != nil {
		anew = cosmoSigmaskL2A(uint64(new[0]) | uint64(new[1])<<32)
		anewp = uintptr(unsafe.Pointer(&anew))
	}
	if old != nil {
		aoldp = uintptr(unsafe.Pointer(&aold))
	}
	if _, errno := cosmoXnuSyscall6(_XNU_sigprocmask, uintptr(how+1), anewp, aoldp, 0, 0, 0); errno != 0 {
		throw("darwinSigprocmask: sigprocmask failed")
	}
	if old != nil {
		m := cosmoSigmaskA2L(aold)
		old[0] = uint32(m)
		old[1] = uint32(m >> 32)
	}
}

// darwinSigaltstack implements sigaltstack on macOS-Intel: translate the
// Linux stackt {sp, flags, pad, size} to and from Apple's user64_sigaltstack
// {sp, size, flags} and SS_DISABLE (2 on Linux, 4 on Apple), then issue
// the raw sigaltstack syscall. arm64 does the same over Apple libc
// (signal_cosmo_xnu.go).
//
// Crashes on failure like the Linux asm path: a stack the kernel did not
// take would surface later as a fault on the wrong stack.
//
//go:nosplit
//go:nowritebarrierrec
func darwinSigaltstack(new, old *stackt) {
	var anew, aold xnuStackt
	var anewp, aoldp uintptr
	if new != nil {
		anew.ss_sp = *(*uintptr)(unsafe.Pointer(&new.ss_sp))
		anew.ss_size = new.ss_size
		var fl int32
		if new.ss_flags&_SS_DISABLE != 0 {
			fl |= xnuSS_DISABLE
		}
		if new.ss_flags&xnuSS_ONSTACK != 0 {
			fl |= xnuSS_ONSTACK
		}
		anew.ss_flags = fl
		anewp = uintptr(unsafe.Pointer(&anew))
	}
	if old != nil {
		aoldp = uintptr(unsafe.Pointer(&aold))
	}
	if _, errno := cosmoXnuSyscall6(_XNU_sigaltstack, anewp, aoldp, 0, 0, 0, 0); errno != 0 {
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
// values differ from Linux's, so the raw syscall path serves Linux and
// NT hosts only.
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
// (sys_cosmo_amd64.s); its NT branch is a no-op.
//
//go:noescape
func sigaltstackLinux(new, old *stackt)
