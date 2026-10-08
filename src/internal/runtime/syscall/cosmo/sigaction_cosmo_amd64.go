// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

package cosmo

import (
	"internal/abi"
	"unsafe"
)

// rt_sigaction emulation for macOS-Intel hosts.

// Errno value (Linux numbering) produced by this emulation itself.
const darwinEINVAL = 22

// xnuKsigactiont is Apple's KERNEL struct sigaction, what __sigaction takes.
type xnuKsigactiont struct {
	handler uintptr
	tramp   uintptr
	mask    uint32
	flags   int32
}

// xnuSigactiont is Apple's user64_sigaction, the action __sigaction copies
// out (XNU kern_sig.c sigaction_kern_to_user64).
type xnuSigactiont struct {
	handler uintptr
	mask    uint32
	flags   int32
}

var sigA2LTab = [32]byte{
	1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6,
	7:  0, // SIGEMT: no Linux equivalent
	8:  8,
	9:  9,
	10: 7, // SIGBUS
	11: 11,
	12: 31, // SIGSYS
	13: 13, 14: 14, 15: 15,
	16: 23, // SIGURG
	17: 19, // SIGSTOP
	18: 20, // SIGTSTP
	19: 18, // SIGCONT
	20: 17, // SIGCHLD
	21: 21, 22: 22,
	23: 29, // SIGIO
	24: 24, 25: 25, 26: 26, 27: 27, 28: 28,
	29: 0,  // SIGINFO: no Linux equivalent
	30: 10, // SIGUSR1
	31: 12, // SIGUSR2
}

// sigactionTramp is the sa_tramp handed to __sigaction.
func sigactionTramp()

//go:noescape
func xnuSigaction(sig uintptr, new, old unsafe.Pointer) (r1, errno uintptr)

// darwinSigaction emulates rt_sigaction with __sigaction. It is called
// from Syscall6's darwin dispatch (asm_cosmo_amd64.s), which is already
// past entersyscall, so nothing on this path may grow the stack.
//
// A signal with no Apple number (SIGSTKFLT, SIGPWR, the realtime range)
// fails with EINVAL rather than reporting a handler this host can never
// deliver. The runtime's own path treats the same case as a no-op
// success, because initsig walks every signal and must not fail. A
// caller naming one signal gets told instead.
//
//go:nosplit
func darwinSigaction(sig, new, old, sigsetsize uintptr) (r1, errno uintptr) {
	if sigsetsize != linuxSigsetSize {
		return ^uintptr(0), darwinEINVAL
	}
	asig, ok := darwinXlatSignal(sig)
	if !ok || asig == 0 {
		return ^uintptr(0), darwinEINVAL
	}
	var anew xnuKsigactiont
	var aold xnuSigactiont
	var anewp, aoldp unsafe.Pointer
	if new != 0 {
		lnew := (*linuxSigactiont)(unsafe.Pointer(new))
		anew.handler = lnew.handler
		anew.flags = sigFlagsL2A(lnew.flags)
		anew.mask = sigmaskL2A(lnew.mask)
		if anew.handler > 1 {
			// Only a real handler needs a trampoline.
			anew.tramp = abi.FuncPCABI0(sigactionTramp)
		}
		anewp = unsafe.Pointer(&anew)
	}
	if old != 0 {
		aoldp = unsafe.Pointer(&aold)
	}
	r1, errno = xnuSigaction(asig, anewp, aoldp)
	if errno != 0 {
		return r1, errno
	}
	if old != 0 {
		lold := (*linuxSigactiont)(unsafe.Pointer(old))
		lold.handler = aold.handler
		lold.flags = sigFlagsA2L(aold.flags)
		// Linux fills sa_restorer from the caller's own SA_RESTORER request; XNU has no counterpart to read one back from.
		lold.restorer = 0
		lold.mask = sigmaskA2L(aold.mask)
	}
	return 0, 0
}
