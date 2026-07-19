// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// sigctxt is HOST-AWARE: one cosmo binary runs on Linux and macOS, and
// the kernel hands the signal handler its native context structure -
// a Linux ucontext with an embedded sigcontext, or an Apple ucontext
// whose uc_mcontext FIELD IS A POINTER to a __darwin_mcontext64 in the
// signal frame. Every accessor dispatches on __hostos (the established
// runtime-dispatch pattern; the Linux branch is byte-for-byte the old
// code). Writes (set_pc/set_sp for sigpanic injection and async
// preemption's pushCall) go through the same dispatch, so on macOS
// they land in the kernel's own mcontext and Apple's sigreturn
// restores them - no context copying or translation is needed.
//
// The Apple layouts below mirror upstream defs_darwin_arm64.go
// (ucontext, mcontext64, regs64, exceptionstate64, siginfo); the
// neonstate64 tail of mcontext64 is never accessed and is omitted.

type sigctxt struct {
	info *siginfo
	ctxt unsafe.Pointer
}

// xnuExceptionState64 is __darwin_arm_exception_state64.
type xnuExceptionState64 struct {
	far uint64 // virtual fault addr
	esr uint32 // exception syndrome
	exc uint32 // number of arm exception taken
}

// xnuRegs64 is __darwin_arm_thread_state64.
type xnuRegs64 struct {
	x    [29]uint64 // registers x0 to x28
	fp   uint64     // frame register, x29
	lr   uint64     // link register, x30
	sp   uint64     // stack pointer, x31
	pc   uint64     // program counter
	cpsr uint32     // current program status register
	_    uint32
}

// xnuMcontext64 is the head of __darwin_mcontext64. The trailing
// neonstate64 (floating point state) is never touched by the runtime
// and is left off; the struct is only ever used via a pointer into
// the kernel-provided signal frame.
type xnuMcontext64 struct {
	es xnuExceptionState64
	ss xnuRegs64
}

// xnuStackt is Apple's stack_t: {sp, size, flags}, unlike Linux
// arm64's {sp, flags, pad, size}.
type xnuStackt struct {
	ss_sp    uintptr
	ss_size  uintptr
	ss_flags int32
	_        [4]byte
}

// xnuUcontext is Apple's ucontext_t. uc_mcontext is a POINTER (at
// offset 48) into the signal frame, where Linux embeds the mcontext
// by value.
type xnuUcontext struct {
	uc_onstack  int32
	uc_sigmask  uint32
	uc_stack    xnuStackt
	uc_link     uintptr
	uc_mcsize   uint64
	uc_mcontext *xnuMcontext64
}

// xnuSiginfo is Apple's siginfo_t. si_signo/si_errno/si_code share
// Linux's offsets (0/4/8); si_addr sits at offset 24 (Linux keeps its
// union at 16).
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
func (c *sigctxt) regs() *sigcontext { return &(*ucontext)(c.ctxt).uc_mcontext }

//go:nosplit
//go:nowritebarrierrec
func (c *sigctxt) xnuRegs() *xnuRegs64 { return &(*xnuUcontext)(c.ctxt).uc_mcontext.ss }

//go:nosplit
func (c *sigctxt) xnuInfo() *xnuSiginfo { return (*xnuSiginfo)(unsafe.Pointer(c.info)) }

//go:nosplit
func (c *sigctxt) reg(i int) uint64 {
	if isdarwin() {
		return c.xnuRegs().x[i]
	}
	return c.regs().regs[i]
}

func (c *sigctxt) r0() uint64  { return c.reg(0) }
func (c *sigctxt) r1() uint64  { return c.reg(1) }
func (c *sigctxt) r2() uint64  { return c.reg(2) }
func (c *sigctxt) r3() uint64  { return c.reg(3) }
func (c *sigctxt) r4() uint64  { return c.reg(4) }
func (c *sigctxt) r5() uint64  { return c.reg(5) }
func (c *sigctxt) r6() uint64  { return c.reg(6) }
func (c *sigctxt) r7() uint64  { return c.reg(7) }
func (c *sigctxt) r8() uint64  { return c.reg(8) }
func (c *sigctxt) r9() uint64  { return c.reg(9) }
func (c *sigctxt) r10() uint64 { return c.reg(10) }
func (c *sigctxt) r11() uint64 { return c.reg(11) }
func (c *sigctxt) r12() uint64 { return c.reg(12) }
func (c *sigctxt) r13() uint64 { return c.reg(13) }
func (c *sigctxt) r14() uint64 { return c.reg(14) }
func (c *sigctxt) r15() uint64 { return c.reg(15) }
func (c *sigctxt) r16() uint64 { return c.reg(16) }
func (c *sigctxt) r17() uint64 { return c.reg(17) }
func (c *sigctxt) r18() uint64 { return c.reg(18) }
func (c *sigctxt) r19() uint64 { return c.reg(19) }
func (c *sigctxt) r20() uint64 { return c.reg(20) }
func (c *sigctxt) r21() uint64 { return c.reg(21) }
func (c *sigctxt) r22() uint64 { return c.reg(22) }
func (c *sigctxt) r23() uint64 { return c.reg(23) }
func (c *sigctxt) r24() uint64 { return c.reg(24) }
func (c *sigctxt) r25() uint64 { return c.reg(25) }
func (c *sigctxt) r26() uint64 { return c.reg(26) }
func (c *sigctxt) r27() uint64 { return c.reg(27) }
func (c *sigctxt) r28() uint64 { return c.reg(28) }

func (c *sigctxt) r29() uint64 {
	if isdarwin() {
		return c.xnuRegs().fp
	}
	return c.regs().regs[29]
}

func (c *sigctxt) lr() uint64 {
	if isdarwin() {
		return c.xnuRegs().lr
	}
	return c.regs().regs[30]
}

//go:nosplit
func (c *sigctxt) sp() uint64 {
	if isdarwin() {
		return c.xnuRegs().sp
	}
	return c.regs().sp
}

//go:nosplit
//go:nowritebarrierrec
func (c *sigctxt) pc() uint64 {
	if isdarwin() {
		return c.xnuRegs().pc
	}
	return c.regs().pc
}

func (c *sigctxt) pstate() uint64 {
	if isdarwin() {
		return uint64(c.xnuRegs().cpsr)
	}
	return c.regs().pstate
}

//go:nosplit
func (c *sigctxt) fault() uintptr {
	if isdarwin() {
		// Like upstream darwin: the fault address from siginfo.
		// (The mcontext's es.far holds the same value.)
		return uintptr(c.xnuInfo().si_addr)
	}
	return uintptr(c.regs().fault_address)
}

func (c *sigctxt) sigcode() uint64 { return uint64(c.info.si_code) } // offset 8 on both systems

//go:nosplit
func (c *sigctxt) sigaddr() uint64 {
	if isdarwin() {
		return c.xnuInfo().si_addr
	}
	return c.info.si_addr
}

func (c *sigctxt) set_pc(x uint64) {
	if isdarwin() {
		c.xnuRegs().pc = x
		return
	}
	c.regs().pc = x
}

func (c *sigctxt) set_sp(x uint64) {
	if isdarwin() {
		c.xnuRegs().sp = x
		return
	}
	c.regs().sp = x
}

func (c *sigctxt) set_lr(x uint64) {
	if isdarwin() {
		c.xnuRegs().lr = x
		return
	}
	c.regs().regs[30] = x
}

func (c *sigctxt) set_r28(x uint64) {
	if isdarwin() {
		c.xnuRegs().x[28] = x
		return
	}
	c.regs().regs[28] = x
}

func (c *sigctxt) set_sigcode(x uint64) { c.info.si_code = int32(x) } // offset 8 on both systems

func (c *sigctxt) set_sigaddr(x uint64) {
	if isdarwin() {
		c.xnuInfo().si_addr = x
		return
	}
	*(*uintptr)(add(unsafe.Pointer(c.info), 2*goarch.PtrSize)) = uintptr(x)
}

//go:nosplit
func (c *sigctxt) fixsigcode(sig uint32) {
	if !isdarwin() {
		return
	}
	// Ported from upstream signal_darwin_arm64.go.
	switch sig {
	case _SIGTRAP:
		// OS X sets c.sigcode() == TRAP_BRKPT unconditionally for all
		// SIGTRAPs, leaving no way to distinguish a breakpoint-induced
		// SIGTRAP from an asynchronous signal SIGTRAP. They all look
		// breakpoint-induced by default. Try looking at the code to see
		// if it's a breakpoint. The assumption is that we're very
		// unlikely to get an asynchronous SIGTRAP at just the moment
		// that the PC started to point at unmapped memory.
		pc := uintptr(c.pc())
		// OS X will leave the pc just after the instruction.
		code := (*uint32)(unsafe.Pointer(pc - 4))
		if *code != 0xd4200000 {
			// SIGTRAP on something other than breakpoint.
			c.set_sigcode(_SI_USER)
		}
	}
}
