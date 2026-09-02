// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// The architecture-dependent half of the NT signal, preemption and
// profiling machinery: the Windows CONTEXT record and the small set of
// operations the shared code performs on it.

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// CONTEXT_AMD64 | CONTEXT_CONTROL: capture SegSs/Rsp/SegCs/Rip/EFlags
// only. asyncPreempt saves everything else itself, so a suspended
// thread's other registers stay untouched.
const _NT_CONTEXT_CONTROL = 0x100001

// ntM128A is the win64 M128A (16 bytes).
type ntM128A struct {
	low  uint64
	high uint64
}

// ntContext is the FULL CONTEXT (x64) layout, 1232 (0x4D0) bytes.
// Offsets match upstream
// internal/runtime/syscall/windows/defs_windows_amd64.go (Rip = 0xF8).
// The VEH handlers only touch fields up to rip on OS-allocated
// records, but ntPreemptM allocates its own buffer for
// GetThreadContext, which requires the complete struct - and a
// 16-byte-aligned base, which Go's 8-byte struct alignment does not
// give; ntPreemptM over-allocates and rounds, upstream's idiom.
type ntContext struct {
	p1home, p2home, p3home, p4home, p5home, p6home uint64
	contextFlags                                   uint32
	mxcsr                                          uint32
	segCs, segDs, segEs, segFs, segGs, segSs       uint16
	eflags                                         uint32
	dr0, dr1, dr2, dr3, dr6, dr7                   uint64
	rax, rcx, rdx, rbx, rsp, rbp, rsi, rdi         uint64
	r8, r9, r10, r11, r12, r13, r14, r15           uint64
	rip                                            uint64
	fltsave                                        [512]byte // XSAVE_FORMAT (legacy FP area)
	vectorRegister                                 [26]ntM128A
	vectorControl                                  uint64
	debugControl                                   uint64
	lastBranchToRip                                uint64
	lastBranchFromRip                              uint64
	lastExceptionToRip                             uint64
	lastExceptionFromRip                           uint64
}

//go:nosplit
func (c *ntContext) getPC() uintptr { return uintptr(c.rip) }

//go:nosplit
func (c *ntContext) getSP() uintptr { return uintptr(c.rsp) }

// lr is 0 on amd64: the return address lives on the stack, and sigprof
// takes the same 0 the unix amd64 signal handler passes.
//
//go:nosplit
func (c *ntContext) getLR() uintptr { return 0 }

//go:nosplit
func (c *ntContext) setPC(x uintptr) { c.rip = uint64(x) }

// pushCall makes the interrupted code look like it called targetPC:
// push resumePC where a CALL would have left it, then point RIP at the
// target. The stack write is a plain store; the target is either
// suspended (preemption) or stopped in the exception dispatcher.
//
//go:nosplit
func (c *ntContext) pushCall(targetPC, resumePC uintptr) {
	sp := uintptr(c.rsp) - goarch.PtrSize
	*(*uintptr)(unsafe.Pointer(sp)) = resumePC
	c.rsp = uint64(sp)
	c.rip = uint64(targetPC)
}

// ntSetSyntheticPCSP fills the synthesized ucontext ntDeliverSelfSignal
// hands the signal machinery. Only the PC and SP are ever read there.
//
//go:nosplit
func ntSetSyntheticPCSP(uc *ucontext, pc, sp uintptr) {
	regs := (*sigcontext)(unsafe.Pointer(&uc.uc_mcontext))
	regs.rip = uint64(pc)
	regs.rsp = uint64(sp)
}

// ntSetTEBg publishes g in the TEB slot the exception trampolines read.
// On amd64 that slot is gs:0x28, which rt0's TLS setup already fills,
// so there is nothing left to do.
//
//go:nosplit
func ntSetTEBg() {}

// ntDumpregs prints the CONTEXT registers (upstream dumpregs,
// signal_windows_amd64.go).
//
//go:nosplit
func ntDumpregs(r *ntContext) {
	print("rax     ", hex(r.rax), "\n")
	print("rbx     ", hex(r.rbx), "\n")
	print("rcx     ", hex(r.rcx), "\n")
	print("rdx     ", hex(r.rdx), "\n")
	print("rdi     ", hex(r.rdi), "\n")
	print("rsi     ", hex(r.rsi), "\n")
	print("rbp     ", hex(r.rbp), "\n")
	print("rsp     ", hex(r.rsp), "\n")
	print("r8      ", hex(r.r8), "\n")
	print("r9      ", hex(r.r9), "\n")
	print("r10     ", hex(r.r10), "\n")
	print("r11     ", hex(r.r11), "\n")
	print("r12     ", hex(r.r12), "\n")
	print("r13     ", hex(r.r13), "\n")
	print("r14     ", hex(r.r14), "\n")
	print("r15     ", hex(r.r15), "\n")
	print("rip     ", hex(r.rip), "\n")
	print("rflags  ", hex(uint64(r.eflags)), "\n")
	print("cs      ", hex(uint64(r.segCs)), "\n")
	print("fs      ", hex(uint64(r.segFs)), "\n")
	print("gs      ", hex(uint64(r.segGs)), "\n")
}
