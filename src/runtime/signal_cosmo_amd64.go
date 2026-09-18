// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// sigctxt is HOST-AWARE. One cosmo binary runs on Linux and macOS, and
// the kernel hands the handler its native context: a Linux ucontext with
// the sigcontext embedded, or an XNU user_ucontext64 whose uc_mcontext64
// field is a POINTER to the mcontext in the signal frame. Every accessor
// dispatches on __hostos. Writes land in the kernel's own mcontext, so
// sigreturn restores them without a copy.
//
// The XNU layouts are x86_exception_state64, x86_thread_state64,
// user_ucontext64 and user64_siginfo (XNU bsd/dev/i386/unix_signal.c,
// bsd/sys/signal.h); Go's pre-1.12 darwin port carries the same structs
// as exceptionstate64, regs64, ucontext and siginfo.

type sigctxt struct {
	info *siginfo
	ctxt unsafe.Pointer
}

// xnuExceptionState64 is x86_exception_state64, the head of the mcontext.
type xnuExceptionState64 struct {
	trapno     uint16
	cpu        uint16
	err        uint32
	faultvaddr uint64
}

// xnuRegs64 is x86_thread_state64. Every field is 64 bits wide, the
// segment selectors included.
type xnuRegs64 struct {
	rax    uint64
	rbx    uint64
	rcx    uint64
	rdx    uint64
	rdi    uint64
	rsi    uint64
	rbp    uint64
	rsp    uint64
	r8     uint64
	r9     uint64
	r10    uint64
	r11    uint64
	r12    uint64
	r13    uint64
	r14    uint64
	r15    uint64
	rip    uint64
	rflags uint64
	cs     uint64
	fs     uint64
	gs     uint64
}

// xnuMcontext64 is the head of the mcontext the kernel writes into the
// signal frame. The float state that follows is never touched and is
// left off; the struct is only used through a pointer into that frame.
type xnuMcontext64 struct {
	es xnuExceptionState64
	ss xnuRegs64
}

// xnuStackt is Apple's stack_t: {sp, size, flags}. Linux amd64 orders it
// {sp, flags, pad, size}.
type xnuStackt struct {
	ss_sp    uintptr
	ss_size  uintptr
	ss_flags int32
	_        [4]byte
}

// xnuUcontext is user_ucontext64. uc_mcontext is a POINTER (offset 48)
// into the signal frame; Linux embeds the mcontext by value.
type xnuUcontext struct {
	uc_onstack  int32
	uc_sigmask  uint32
	uc_stack    xnuStackt
	uc_link     uintptr
	uc_mcsize   uint64
	uc_mcontext *xnuMcontext64
}

// xnuSiginfo is user64_siginfo. si_signo/si_errno/si_code share Linux's
// offsets (0/4/8); si_addr sits at offset 24 (Linux keeps its union at
// 16).
type xnuSiginfo struct {
	si_signo  int32
	si_errno  int32
	si_code   int32
	si_pid    int32
	si_uid    uint32
	si_status int32
	si_addr   uint64
	si_value  [8]byte
	si_band   int64
	_         [7]uint64
}

//go:nosplit
//go:nowritebarrierrec
func (c *sigctxt) regs() *sigcontext {
	return (*sigcontext)(unsafe.Pointer(&(*ucontext)(c.ctxt).uc_mcontext))
}

//go:nosplit
//go:nowritebarrierrec
func (c *sigctxt) xnuRegs() *xnuRegs64 { return &(*xnuUcontext)(c.ctxt).uc_mcontext.ss }

//go:nosplit
func (c *sigctxt) xnuInfo() *xnuSiginfo { return (*xnuSiginfo)(unsafe.Pointer(c.info)) }

func (c *sigctxt) rax() uint64 {
	if isdarwin() {
		return c.xnuRegs().rax
	}
	return c.regs().rax
}

func (c *sigctxt) rbx() uint64 {
	if isdarwin() {
		return c.xnuRegs().rbx
	}
	return c.regs().rbx
}

func (c *sigctxt) rcx() uint64 {
	if isdarwin() {
		return c.xnuRegs().rcx
	}
	return c.regs().rcx
}

func (c *sigctxt) rdx() uint64 {
	if isdarwin() {
		return c.xnuRegs().rdx
	}
	return c.regs().rdx
}

func (c *sigctxt) rdi() uint64 {
	if isdarwin() {
		return c.xnuRegs().rdi
	}
	return c.regs().rdi
}

func (c *sigctxt) rsi() uint64 {
	if isdarwin() {
		return c.xnuRegs().rsi
	}
	return c.regs().rsi
}

func (c *sigctxt) rbp() uint64 {
	if isdarwin() {
		return c.xnuRegs().rbp
	}
	return c.regs().rbp
}

//go:nosplit
func (c *sigctxt) rsp() uint64 {
	if isdarwin() {
		return c.xnuRegs().rsp
	}
	return c.regs().rsp
}

func (c *sigctxt) r8() uint64 {
	if isdarwin() {
		return c.xnuRegs().r8
	}
	return c.regs().r8
}

func (c *sigctxt) r9() uint64 {
	if isdarwin() {
		return c.xnuRegs().r9
	}
	return c.regs().r9
}

func (c *sigctxt) r10() uint64 {
	if isdarwin() {
		return c.xnuRegs().r10
	}
	return c.regs().r10
}

func (c *sigctxt) r11() uint64 {
	if isdarwin() {
		return c.xnuRegs().r11
	}
	return c.regs().r11
}

func (c *sigctxt) r12() uint64 {
	if isdarwin() {
		return c.xnuRegs().r12
	}
	return c.regs().r12
}

func (c *sigctxt) r13() uint64 {
	if isdarwin() {
		return c.xnuRegs().r13
	}
	return c.regs().r13
}

func (c *sigctxt) r14() uint64 {
	if isdarwin() {
		return c.xnuRegs().r14
	}
	return c.regs().r14
}

func (c *sigctxt) r15() uint64 {
	if isdarwin() {
		return c.xnuRegs().r15
	}
	return c.regs().r15
}

//go:nosplit
//go:nowritebarrierrec
func (c *sigctxt) rip() uint64 {
	if isdarwin() {
		return c.xnuRegs().rip
	}
	return c.regs().rip
}

func (c *sigctxt) rflags() uint64 {
	if isdarwin() {
		return c.xnuRegs().rflags
	}
	return c.regs().eflags
}

func (c *sigctxt) cs() uint64 {
	if isdarwin() {
		return c.xnuRegs().cs
	}
	return uint64(c.regs().cs)
}

func (c *sigctxt) fs() uint64 {
	if isdarwin() {
		return c.xnuRegs().fs
	}
	return uint64(c.regs().fs)
}

func (c *sigctxt) gs() uint64 {
	if isdarwin() {
		return c.xnuRegs().gs
	}
	return uint64(c.regs().gs)
}

func (c *sigctxt) sigcode() uint64 { return uint64(c.info.si_code) } // offset 8 on both systems

//go:nosplit
func (c *sigctxt) sigaddr() uint64 {
	if isdarwin() {
		return c.xnuInfo().si_addr
	}
	return c.info.si_addr
}

func (c *sigctxt) set_rip(x uint64) {
	if isdarwin() {
		c.xnuRegs().rip = x
		return
	}
	c.regs().rip = x
}

func (c *sigctxt) set_rsp(x uint64) {
	if isdarwin() {
		c.xnuRegs().rsp = x
		return
	}
	c.regs().rsp = x
}

func (c *sigctxt) set_sigcode(x uint64) { c.info.si_code = int32(x) } // offset 8 on both systems

func (c *sigctxt) set_sigaddr(x uint64) {
	if isdarwin() {
		c.xnuInfo().si_addr = x
		return
	}
	*(*uintptr)(add(unsafe.Pointer(c.info), 2*goarch.PtrSize)) = uintptr(x)
}

// Apple SIGFPE si_code values (Go's pre-1.12 defs_darwin_amd64.go). The
// Linux values the runtime compares against are in defs_cosmo_amd64.go.
// The ILL, TRAP, BUS and SEGV codes agree on both systems; the FPE codes
// do not.
const (
	xnuFPE_INTDIV = 0x7
	xnuFPE_INTOVF = 0x8
	xnuFPE_FLTDIV = 0x1
	xnuFPE_FLTOVF = 0x2
	xnuFPE_FLTUND = 0x3
	xnuFPE_FLTRES = 0x4
	xnuFPE_FLTINV = 0x5
	xnuFPE_FLTSUB = 0x6
)

// xnuFPECodeA2L translates an Apple SIGFPE si_code to the Linux value.
// An unknown code (Apple's FPE_NOOP is 0) passes through.
//
//go:nosplit
func xnuFPECodeA2L(code uint64) uint64 {
	switch code {
	case xnuFPE_INTDIV:
		return _FPE_INTDIV
	case xnuFPE_INTOVF:
		return _FPE_INTOVF
	case xnuFPE_FLTDIV:
		return _FPE_FLTDIV
	case xnuFPE_FLTOVF:
		return _FPE_FLTOVF
	case xnuFPE_FLTUND:
		return _FPE_FLTUND
	case xnuFPE_FLTRES:
		return _FPE_FLTRES
	case xnuFPE_FLTINV:
		return _FPE_FLTINV
	case xnuFPE_FLTSUB:
		return _FPE_FLTSUB
	}
	return code
}

// fixsigcode acts on XNU hosts only. The SIGTRAP and SIGSEGV cases are
// upstream signal_darwin_amd64.go. The SIGFPE case remaps Apple's codes
// so sigpanic tells an integer divide from a float fault.
//
//go:nosplit
func (c *sigctxt) fixsigcode(sig uint32) {
	if !isdarwin() {
		return
	}
	switch sig {
	case _SIGTRAP:
		// OS X sets c.sigcode() == TRAP_BRKPT unconditionally for all SIGTRAPs,
		// leaving no way to distinguish a breakpoint-induced SIGTRAP
		// from an asynchronous signal SIGTRAP.
		// They all look breakpoint-induced by default.
		// Try looking at the code to see if it's a breakpoint.
		// The assumption is that we're very unlikely to get an
		// asynchronous SIGTRAP at just the moment that the
		// PC started to point at unmapped memory.
		pc := uintptr(c.rip())
		// OS X will leave the pc just after the INT 3 instruction.
		// INT 3 is usually 1 byte, but there is a 2-byte form.
		code := (*[2]byte)(unsafe.Pointer(pc - 2))
		if code[1] != 0xCC && (code[0] != 0xCD || code[1] != 3) {
			// SIGTRAP on something other than INT 3.
			c.set_sigcode(_SI_USER)
		}

	case _SIGSEGV:
		// x86-64 has 48-bit virtual addresses. The top 16 bits must echo bit 47.
		// The hardware delivers a different kind of fault for a malformed address
		// than it does for an attempt to access a valid but unmapped address.
		// OS X 10.9.2 mishandles the malformed address case, making it look like
		// a user-generated signal (like someone ran kill -SEGV ourpid).
		// We pass user-generated signals to os/signal, or else ignore them.
		// Doing that here - and returning to the faulting code - results in an
		// infinite loop. It appears the best we can do is rewrite what the kernel
		// delivers into something more like the truth. The address used below
		// has very little chance of being the one that caused the fault, but it is
		// malformed, it is clearly not a real pointer, and if it does get printed
		// in real life, people will probably search for it and find this code.
		if c.sigcode() == _SI_USER {
			c.set_sigcode(_SI_USER + 1)
			c.set_sigaddr(0xb01dfacedebac1e)
		}

	case _SIGFPE:
		c.set_sigcode(xnuFPECodeA2L(c.sigcode()))
	}
}
