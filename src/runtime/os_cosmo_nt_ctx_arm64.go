// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

// The architecture-dependent half of the NT signal, preemption and
// profiling machinery: the Windows CONTEXT record and the small set of
// operations the shared code performs on it. Layout and flag value
// come from upstream
// internal/runtime/syscall/windows/defs_windows_arm64.go.

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// CONTEXT_ARM64 | CONTEXT_CONTROL | CONTEXT_INTEGER. Upstream's note:
// CONTEXT_CONTROL alone is 0x400001, but on Windows 10 LR does not
// come along without CONTEXT_INTEGER, and a missing LR skips the
// next-to-bottom frame of a profile when the bottom frame is
// frameless.
const _NT_CONTEXT_CONTROL = 0x400003

// ntContext is the ARM64_NT_CONTEXT layout, declared in
// os_cosmo_nt_ctx_arm64_layout.go. The VEH handlers only read pc, sp
// and x[28] on OS-allocated records, but ntPreemptM allocates its own
// buffer for GetThreadContext, which requires the complete struct.
type ntContext = ntContextARM64

//go:nosplit
func (c *ntContext) getPC() uintptr { return uintptr(c.pc) }

//go:nosplit
func (c *ntContext) getSP() uintptr { return uintptr(c.xsp) }

//go:nosplit
func (c *ntContext) getLR() uintptr { return uintptr(c.x[30]) }

//go:nosplit
func (c *ntContext) setPC(x uintptr) { c.pc = uint64(x) }

// pushCall makes the interrupted code look like it called targetPC.
// arm64 passes the return address in LR, so the old LR goes to the
// stack and gentraceback knows about the extra slot (sigctxt.pushCall,
// signal_arm64.go). Upstream windows Context.PushCall, verbatim.
//
//go:nosplit
func (c *ntContext) pushCall(targetPC, resumePC uintptr) {
	sp := c.getSP() - goarch.StackAlign
	c.xsp = uint64(sp)
	*(*uint64)(unsafe.Pointer(sp)) = uint64(c.getLR())
	c.x[30] = uint64(resumePC)
	c.pc = uint64(targetPC)
}

// ntSetSyntheticPCSP fills the synthesized ucontext ntDeliverSelfSignal
// hands the signal machinery. Only the PC and SP are ever read there.
//
//go:nosplit
func ntSetSyntheticPCSP(uc *ucontext, pc, sp uintptr) {
	regs := (*sigcontext)(unsafe.Pointer(&uc.uc_mcontext))
	regs.pc = uint64(pc)
	regs.sp = uint64(sp)
}

// ntSetTEBg publishes g in this thread's TEB ArbitraryUserPointer. It
// runs on g0 at boot, so the slot holds the thread's g0; ntsigtramp
// falls back to it for a fault in foreign code, where x28 is not g.
// Implemented in sys_cosmo_nt_arm64.s.
func ntSetTEBg()

// ntDumpregs prints the CONTEXT registers (upstream dumpregs,
// signal_windows_arm64.go).
//
//go:nosplit
func ntDumpregs(r *ntContext) {
	for i := 0; i < 29; i++ {
		print("r", i, "   ", hex(r.x[i]), "\n")
	}
	print("r29  ", hex(r.x[29]), "\n")
	print("lr   ", hex(r.x[30]), "\n")
	print("sp   ", hex(r.xsp), "\n")
	print("pc   ", hex(r.pc), "\n")
	print("cpsr ", hex(r.cpsr), "\n")
}
