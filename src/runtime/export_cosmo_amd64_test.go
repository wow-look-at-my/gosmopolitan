// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import "unsafe"

// Exports for signal_cosmo_xnu_amd64_test.go: the XNU x86-64 layouts
// sigctxt and the darwin sigaction/sigaltstack paths read and write,
// surfaced over plain integers since the struct types are unexported.

const (
	XnuUcontextSize       = unsafe.Sizeof(xnuUcontext{})
	XnuUcontextOnstackOff = unsafe.Offsetof(xnuUcontext{}.uc_onstack)
	XnuUcontextSigmaskOff = unsafe.Offsetof(xnuUcontext{}.uc_sigmask)
	XnuUcontextStackOff   = unsafe.Offsetof(xnuUcontext{}.uc_stack)
	XnuUcontextLinkOff    = unsafe.Offsetof(xnuUcontext{}.uc_link)
	XnuUcontextMcsizeOff  = unsafe.Offsetof(xnuUcontext{}.uc_mcsize)
	XnuUcontextMcontextOf = unsafe.Offsetof(xnuUcontext{}.uc_mcontext)

	XnuStacktSize     = unsafe.Sizeof(xnuStackt{})
	XnuStacktSpOff    = unsafe.Offsetof(xnuStackt{}.ss_sp)
	XnuStacktSizeOff  = unsafe.Offsetof(xnuStackt{}.ss_size)
	XnuStacktFlagsOff = unsafe.Offsetof(xnuStackt{}.ss_flags)

	XnuExceptionState64Size = unsafe.Sizeof(xnuExceptionState64{})
	XnuMcontext64SsOff      = unsafe.Offsetof(xnuMcontext64{}.ss)
	XnuMcontext64Size       = unsafe.Sizeof(xnuMcontext64{})

	XnuRegs64RaxOff    = unsafe.Offsetof(xnuRegs64{}.rax)
	XnuRegs64RbxOff    = unsafe.Offsetof(xnuRegs64{}.rbx)
	XnuRegs64RcxOff    = unsafe.Offsetof(xnuRegs64{}.rcx)
	XnuRegs64RdxOff    = unsafe.Offsetof(xnuRegs64{}.rdx)
	XnuRegs64RdiOff    = unsafe.Offsetof(xnuRegs64{}.rdi)
	XnuRegs64RsiOff    = unsafe.Offsetof(xnuRegs64{}.rsi)
	XnuRegs64RbpOff    = unsafe.Offsetof(xnuRegs64{}.rbp)
	XnuRegs64RspOff    = unsafe.Offsetof(xnuRegs64{}.rsp)
	XnuRegs64R8Off     = unsafe.Offsetof(xnuRegs64{}.r8)
	XnuRegs64R15Off    = unsafe.Offsetof(xnuRegs64{}.r15)
	XnuRegs64RipOff    = unsafe.Offsetof(xnuRegs64{}.rip)
	XnuRegs64RflagsOff = unsafe.Offsetof(xnuRegs64{}.rflags)
	XnuRegs64CsOff     = unsafe.Offsetof(xnuRegs64{}.cs)
	XnuRegs64FsOff     = unsafe.Offsetof(xnuRegs64{}.fs)
	XnuRegs64GsOff     = unsafe.Offsetof(xnuRegs64{}.gs)
	XnuRegs64Size      = unsafe.Sizeof(xnuRegs64{})

	XnuSiginfoSize      = unsafe.Sizeof(xnuSiginfo{})
	XnuSiginfoSignoOff  = unsafe.Offsetof(xnuSiginfo{}.si_signo)
	XnuSiginfoErrnoOff  = unsafe.Offsetof(xnuSiginfo{}.si_errno)
	XnuSiginfoCodeOff   = unsafe.Offsetof(xnuSiginfo{}.si_code)
	XnuSiginfoPidOff    = unsafe.Offsetof(xnuSiginfo{}.si_pid)
	XnuSiginfoUidOff    = unsafe.Offsetof(xnuSiginfo{}.si_uid)
	XnuSiginfoStatusOff = unsafe.Offsetof(xnuSiginfo{}.si_status)
	XnuSiginfoAddrOff   = unsafe.Offsetof(xnuSiginfo{}.si_addr)
	XnuSiginfoValueOff  = unsafe.Offsetof(xnuSiginfo{}.si_value)
	XnuSiginfoBandOff   = unsafe.Offsetof(xnuSiginfo{}.si_band)

	XnuKsigactiontSize       = unsafe.Sizeof(xnuKsigactiont{})
	XnuKsigactiontTrampOff   = unsafe.Offsetof(xnuKsigactiont{}.sa_tramp)
	XnuSigactiontSize        = unsafe.Sizeof(xnuSigactiont{})
	XnuSigactiontHandlerOff  = unsafe.Offsetof(xnuSigactiont{}.sa_handler)
	XnuSigactiontMaskOff     = unsafe.Offsetof(xnuSigactiont{}.sa_mask)
	XnuSigactiontFlagsOff    = unsafe.Offsetof(xnuSigactiont{}.sa_flags)
	LinuxStacktSize          = unsafe.Sizeof(stackt{})
	LinuxStacktFlagsOff      = unsafe.Offsetof(stackt{}.ss_flags)
	LinuxStacktSizeOff       = unsafe.Offsetof(stackt{}.ss_size)
	LinuxSiginfoAddrOff      = unsafe.Offsetof(siginfo{}.si_addr)
	LinuxSigcontextRipOff    = unsafe.Offsetof(sigcontext{}.rip)
	LinuxUcontextMcontextOff = unsafe.Offsetof(ucontext{}.uc_mcontext)

	XnuSSOnstack = xnuSS_ONSTACK
	XnuSSDisable = xnuSS_DISABLE
	LinuxSSDisab = _SS_DISABLE
)

var XnuFPECodeA2L = xnuFPECodeA2L

// CosmoXlatErrno is the amd64 Apple-to-Linux errno translation
// (cosmo_xlat_errno_ax over cosmo_errno_xlat_tab).
var CosmoXlatErrno = cosmoXlatErrno
