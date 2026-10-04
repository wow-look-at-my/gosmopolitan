// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// rt_sigaction emulation for macOS ARM64 hosts.

// xnuSigactiont is Apple's LIBC struct sigaction.
type xnuSigactiont struct {
	handler uintptr
	mask    uint32
	flags   int32
}

// darwinSigactionSyslib emulates rt_sigaction with the Syslib's sigaction,
// whose address the assembly dispatch reads out of the Syslib table and
// passes in fn: this package cannot reach runtime.__syslib from Go, and
// DarwinFns holds only dlsym entries. It is called from Syscall6's darwin
// path, so it keeps that result shape and must not grow the stack.
//
// A signal with no Apple number fails EINVAL rather than reporting a handler
// this host can never deliver. The runtime's own path answers no-op success
// there, because initsig walks every signal.
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
		return ^uintptr(0), 0, darwinEINVAL
	}
	var anew, aold xnuSigactiont
	var anewp, aoldp uintptr
	if new != 0 {
		lnew := (*linuxSigactiont)(unsafe.Pointer(new))
		anew.handler = lnew.handler
		anew.flags = sigFlagsL2A(lnew.flags)
		anew.mask = sigmaskL2A(lnew.mask)
		anewp = uintptr(unsafe.Pointer(&anew))
	}
	if old != 0 {
		aoldp = uintptr(unsafe.Pointer(&aold))
	}
	if r := int64(darwinLibcCall6(fn, asig, anewp, aoldp, 0, 0, 0)); r < 0 {
		return ^uintptr(0), 0, xlatErrnoDarwin(uintptr(-r))
	}
	if old != 0 {
		lold := (*linuxSigactiont)(unsafe.Pointer(old))
		lold.handler = aold.handler
		lold.flags = sigFlagsA2L(aold.flags)
		// Linux fills sa_restorer from the caller's own SA_RESTORER request; Apple has no counterpart to read one back from.
		lold.restorer = 0
		lold.mask = sigmaskA2L(aold.mask)
	}
	return 0, 0, 0
}
