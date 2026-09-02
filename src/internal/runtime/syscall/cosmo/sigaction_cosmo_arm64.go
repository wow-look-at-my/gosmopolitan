// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// rt_sigaction emulation for macOS ARM64 hosts.
//
// The Syslib's sigaction (offset 272) is a sysret-wrapped passthrough
// to Apple libc sigaction: it takes Apple's LIBC struct sigaction,
// Apple signal numbers and Apple flag values, and it returns 0 or
// -errno rather than -1 with errno set. Every one of those differs from
// what rt_sigaction's caller passes, so the arguments cannot be
// forwarded: the two structs are 16 and 32 bytes with no field at a
// shared offset, and libc would read the Linux sa_flags word as its
// sa_mask.
//
// Apple libc supplies the signal trampoline itself, which is why
// nothing here matches the sa_tramp the amd64 side has to build.

// xnuSigactiont is Apple's LIBC struct sigaction, matching
// runtime.xnuSigactiont in signal_cosmo_xnu.go (upstream
// defs_darwin_arm64.go calls it usigactiont).
type xnuSigactiont struct {
	handler uintptr
	mask    uint32
	flags   int32
}

// darwinSigactionSyslib emulates rt_sigaction with the Syslib's
// sigaction, whose address the assembly dispatch reads out of the
// Syslib table and passes in fn (this package cannot reach
// runtime.__syslib from Go, and DarwinFns holds only dlsym-resolved
// entries).
//
// It is called from Syscall6's darwin path, so it keeps that function's
// result shape and must not grow the stack.
//
// A signal with no Apple number (SIGSTKFLT, SIGPWR, the realtime range)
// fails with EINVAL rather than reporting a handler this host can never
// deliver. The runtime's own path treats the same case as a no-op
// success, because initsig walks every signal and must not fail.
//
//go:nosplit
func darwinSigactionSyslib(fn, sig, new, old, sigsetsize uintptr) (r1, r2, errno uintptr) {
	if fn == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if sigsetsize != linuxSigsetSize {
		return ^uintptr(0), 0, darwinEINVAL
	}
	asig, ok := darwinXlatSignal(sig)
	if !ok || asig == 0 {
		// Signal 0 maps to 0 and is not a signal to install on.
		return ^uintptr(0), 0, darwinEINVAL
	}
	var anew, aold xnuSigactiont
	var anewp, aoldp uintptr
	if new != 0 {
		lnew := (*linuxSigactiont)(unsafe.Pointer(new))
		// SIG_DFL (0) and SIG_IGN (1) have the same values on both
		// systems; a handler address passes through untranslated.
		anew.handler = lnew.handler
		anew.flags = sigFlagsL2A(lnew.flags)
		anew.mask = sigmaskL2A(lnew.mask)
		anewp = uintptr(unsafe.Pointer(&anew))
	}
	if old != 0 {
		aoldp = uintptr(unsafe.Pointer(&aold))
	}
	// Sysret-wrapped: 0 or -errno in Apple numbering.
	if r := int64(darwinLibcCall6(fn, asig, anewp, aoldp, 0, 0, 0)); r < 0 {
		return ^uintptr(0), 0, xlatErrnoDarwin(uintptr(-r))
	}
	if old != 0 {
		lold := (*linuxSigactiont)(unsafe.Pointer(old))
		lold.handler = aold.handler
		lold.flags = sigFlagsA2L(aold.flags)
		// Linux fills sa_restorer from the caller's own SA_RESTORER
		// request; Apple has no counterpart to read one back from.
		lold.restorer = 0
		lold.mask = sigmaskA2L(aold.mask)
	}
	return 0, 0, 0
}
