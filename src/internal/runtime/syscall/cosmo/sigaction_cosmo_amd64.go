// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package cosmo

import (
	"internal/abi"
	"unsafe"
)

// rt_sigaction emulation for macOS-Intel hosts.
//
// XNU has no rt_sigaction. There is no Syslib here either, so this
// issues the raw __sigaction syscall, which takes the KERNEL struct
// sigaction. That is a different interface, not the Linux one renamed:
//
//   - The signal numbers differ for the BSD-heritage signals, so both
//     the signal argument and every bit of the mask are remapped
//     (sigaction_cosmo.go).
//   - The kernel struct carries an sa_tramp field the Linux struct does
//     not, and a real handler cannot be installed without one. The
//     kernel enters that trampoline instead of the handler and expects
//     it to call sigreturn afterwards.
//
// The runtime installs its own handlers through
// runtime.darwinSigaction, which does the same translation over the
// same numbers. It cannot be shared: this package must not import the
// runtime, and its trampoline dispatches through runtime.sigtramp,
// which is right for the runtime's handlers and wrong for a caller's.

// Errno value (Linux numbering) produced by this emulation itself.
const darwinEINVAL = 22

// xnuKsigactiont is Apple's KERNEL struct sigaction, what __sigaction
// takes. Apple's LIBC struct sigaction has no sa_tramp: libc fills the
// field in on the caller's behalf and a raw caller must supply it.
// Layout from Go's pre-1.12 darwin port (go1.8
// runtime/defs_darwin_amd64.go, type sigactiont).
type xnuKsigactiont struct {
	handler uintptr
	tramp   uintptr
	mask    uint32
	flags   int32
}

// sigA2LTab maps an Apple signal number (index 1..31) to the Linux
// number, 0 if there is none. sigactionTramp indexes it directly, which
// is why the correspondence is a table here and a switch in
// darwinXlatSignalA2L; TestSigA2LTab pins the two together.
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

// sigactionTramp is the sa_tramp handed to __sigaction. The KERNEL
// enters it, so the declaration exists to take its address and for
// asmdecl. Defined in asm_cosmo_amd64.s.
func sigactionTramp()

// xnuSigaction issues the raw __sigaction syscall. errno comes back in
// Linux numbering, r1 is -1 on failure. Defined in asm_cosmo_amd64.s.
//
//go:noescape
func xnuSigaction(sig uintptr, new, old unsafe.Pointer) (r1, errno uintptr)

// darwinSigaction emulates rt_sigaction with __sigaction. It is called
// from Syscall6's darwin dispatch (asm_cosmo_amd64.s), which is already
// past entersyscall, so nothing on this path may grow the stack.
//
// A signal with no Apple number (SIGSTKFLT, SIGPWR, the realtime range)
// fails with EINVAL rather than reporting a handler this host can never
// deliver. The runtime's own path treats the same case as a no-op
// success, because initsig walks every signal and must not fail; a
// caller naming one signal gets told instead.
//
//go:nosplit
func darwinSigaction(sig, new, old, sigsetsize uintptr) (r1, errno uintptr) {
	if sigsetsize != linuxSigsetSize {
		return ^uintptr(0), darwinEINVAL
	}
	asig, ok := darwinXlatSignal(sig)
	if !ok || asig == 0 {
		// Signal 0 maps to 0 and is not a signal to install on.
		return ^uintptr(0), darwinEINVAL
	}
	var anew, aold xnuKsigactiont
	var anewp, aoldp unsafe.Pointer
	if new != 0 {
		lnew := (*linuxSigactiont)(unsafe.Pointer(new))
		// SIG_DFL (0) and SIG_IGN (1) have the same values on both
		// systems; a handler address passes through untranslated.
		anew.handler = lnew.handler
		anew.flags = sigFlagsL2A(lnew.flags)
		anew.mask = sigmaskL2A(lnew.mask)
		if anew.handler > 1 {
			// Only a real handler needs a trampoline. Giving one to
			// SIG_DFL or SIG_IGN would hand the kernel a return path
			// for a delivery it never makes.
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
		// Linux fills sa_restorer from the caller's own SA_RESTORER
		// request; XNU has no counterpart to read one back from.
		lold.restorer = 0
		lold.mask = sigmaskA2L(aold.mask)
	}
	return 0, 0
}
