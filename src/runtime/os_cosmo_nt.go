// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Windows NT personality for cosmo/amd64 (wave 1).
//
// Everything in this file is gated on iswindows() (__hostos ==
// _HOSTWINDOWS) and is inert until the NT boot stub (_rt0_cosmo_nt)
// stops exiting early and joins the common boot with __hostos = 2.
//
// Foreign-call model: win64 functions are reached through
// runtime·ntcall6 (sys_cosmo_nt_amd64.s), a SysV->win64 trampoline
// invoked via asmcgocall so the g0 stack switch and stack accounting
// come for free. The function-pointer table is resolved at osArchInit
// time from the two loader-filled IAT slots (GetProcAddress and
// LoadLibraryA, rt0_cosmo_nt_amd64.s), mirroring the darwin port's
// dlsym-at-osArchInit idiom.
//
// Crash pokes (all *(addr) = addr stores, so the fault address names
// the failure; same idiom as the 0xf1 pokes in sys_cosmo_amd64.s):
//
//	0xf2  LoadLibraryA("kernel32.dll") failed
//	0xf3  GetProcAddress(kernel32, ...) missing symbol
//	0xf4  LoadLibraryA("api-ms-win-core-synch-l1-2-0.dll") failed
//	0xf5  GetProcAddress(synch dll, ...) missing symbol
//	0xf6  VirtualFree(MEM_RELEASE) failed (sysFreeOS, mem_cosmo.go)
//	0xf7  rawSyscallNoError reached on NT (src/syscall asm)
//	0xf8  rawVforkSyscall reached on NT (src/syscall asm)

package runtime

import (
	"internal/abi"
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// ntiat is the PE import address table (rt0_cosmo_nt_amd64.s). The NT
// loader overwrites the slots before entry:
//
//	ntiat[0] = &kernel32!GetProcAddress
//	ntiat[1] = &kernel32!LoadLibraryA
//
//go:linkname ntiat
var ntiat [3]uintptr

// Resolved win64 function pointers. Plain variables (not a struct) so
// the assembly NT branches in sys_cosmo_amd64.s can reference them
// directly by symbol name with no offset-rot risk, mirroring the
// cosmoPthread*Fn precedent on arm64.
var (
	ntVirtualAllocFn        uintptr
	ntVirtualFreeFn         uintptr
	ntWriteFileFn           uintptr
	ntGetStdHandleFn        uintptr
	ntExitProcessFn         uintptr // asm: runtime·exit
	ntExitThreadFn          uintptr // asm: runtime·exitThread
	ntCreateThreadFn        uintptr
	ntSleepFn               uintptr // asm: usleep, osyield
	ntGetSystemInfoFn       uintptr
	ntWaitOnAddressFn       uintptr
	ntWakeByAddressSingleFn uintptr

	// Cached std handles (GetStdHandle(-11)/(-12)).
	ntStdout uintptr
	ntStderr uintptr
)

// C string constants for resolution. Package-level []byte("...") vars
// are statically initialized by the compiler (same pattern as
// urandom_dev), so taking &x[0] never allocates - osArchInit runs
// before mallocinit.
var (
	ntNameKernel32       = []byte("kernel32.dll\x00")
	ntNameSynchDLL       = []byte("api-ms-win-core-synch-l1-2-0.dll\x00")
	ntNameVirtualAlloc   = []byte("VirtualAlloc\x00")
	ntNameVirtualFree    = []byte("VirtualFree\x00")
	ntNameWriteFile      = []byte("WriteFile\x00")
	ntNameGetStdHandle   = []byte("GetStdHandle\x00")
	ntNameExitProcess    = []byte("ExitProcess\x00")
	ntNameExitThread     = []byte("ExitThread\x00")
	ntNameCreateThread   = []byte("CreateThread\x00")
	ntNameSleep          = []byte("Sleep\x00")
	ntNameGetSystemInfo  = []byte("GetSystemInfo\x00")
	ntNameWaitOnAddress  = []byte("WaitOnAddress\x00")
	ntNameWakeByAddrSing = []byte("WakeByAddressSingle\x00")
)

// Win32 constants used by wave 1 that only amd64 code references (the
// memory constants live in os_cosmo.go because mem_cosmo.go is shared
// with arm64).
const (
	_NT_STACK_SIZE_PARAM_IS_A_RESERVATION = 0x10000

	_NT_INFINITE = 0xFFFFFFFF

	_NT_STD_OUTPUT_HANDLE = 0xFFFFFFF5 // (DWORD)-11, zero-extended
	_NT_STD_ERROR_HANDLE  = 0xFFFFFFF4 // (DWORD)-12, zero-extended
)

// ntcallArgs is the argument block ntcall packs for the ntcall6
// trampoline. Field offsets are exported to assembly via go_asm.h.
type ntcallArgs struct {
	fn  uintptr
	a1  uintptr
	a2  uintptr
	a3  uintptr
	a4  uintptr
	a5  uintptr
	a6  uintptr
	ret uintptr
}

// Implemented in sys_cosmo_nt_amd64.s.
func ntcall6()
func tstart_cosmo_nt()
func ntwrite1tramp(fd uintptr, p unsafe.Pointer, n int32) int32

// ntcall calls the win64 function fn with up to six integer arguments
// through the ntcall6 trampoline via asmcgocall. Functions taking
// fewer arguments ignore the extra registers/slots (the darwin
// cosmoLibcCall6 convention). The args struct lives on the stack
// (asmcgocall is //go:noescape), so ntcall is usable before
// mallocinit and from nosplit contexts.
//
//go:nosplit
func ntcall(fn, a1, a2, a3, a4, a5, a6 uintptr) uintptr {
	args := ntcallArgs{fn: fn, a1: a1, a2: a2, a3: a3, a4: a4, a5: a5, a6: a6}
	asmcgocall(unsafe.Pointer(abi.FuncPCABI0(ntcall6)), unsafe.Pointer(&args))
	return args.ret
}

//go:nosplit
func ntCrash(code uintptr) {
	*(*uintptr)(unsafe.Pointer(code)) = code
}

// ntResolve fills the function-pointer table using the loader-filled
// IAT slots. Called once from osArchInit on NT hosts, before any other
// NT branch can run. No allocations (pre-mallocinit).
func ntResolve() {
	gpa := ntiat[0] // &GetProcAddress
	lla := ntiat[1] // &LoadLibraryA

	k32 := ntcall(lla, uintptr(unsafe.Pointer(&ntNameKernel32[0])), 0, 0, 0, 0, 0)
	if k32 == 0 {
		ntCrash(0xf2)
	}
	k32sym := func(name *byte) uintptr {
		fn := ntcall(gpa, k32, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0)
		if fn == 0 {
			ntCrash(0xf3)
		}
		return fn
	}
	ntVirtualAllocFn = k32sym(&ntNameVirtualAlloc[0])
	ntVirtualFreeFn = k32sym(&ntNameVirtualFree[0])
	ntWriteFileFn = k32sym(&ntNameWriteFile[0])
	ntGetStdHandleFn = k32sym(&ntNameGetStdHandle[0])
	ntExitProcessFn = k32sym(&ntNameExitProcess[0])
	ntExitThreadFn = k32sym(&ntNameExitThread[0])
	ntCreateThreadFn = k32sym(&ntNameCreateThread[0])
	ntSleepFn = k32sym(&ntNameSleep[0])
	ntGetSystemInfoFn = k32sym(&ntNameGetSystemInfo[0])

	// WaitOnAddress and friends live in the api-ms-win-core-synch
	// forwarder DLL (Win8+; real cosmo imports the same one).
	synch := ntcall(lla, uintptr(unsafe.Pointer(&ntNameSynchDLL[0])), 0, 0, 0, 0, 0)
	if synch == 0 {
		ntCrash(0xf4)
	}
	ntWaitOnAddressFn = ntcall(gpa, synch, uintptr(unsafe.Pointer(&ntNameWaitOnAddress[0])), 0, 0, 0, 0)
	ntWakeByAddressSingleFn = ntcall(gpa, synch, uintptr(unsafe.Pointer(&ntNameWakeByAddrSing[0])), 0, 0, 0, 0)
	if ntWaitOnAddressFn == 0 || ntWakeByAddressSingleFn == 0 {
		ntCrash(0xf5)
	}

	ntStdout = ntcall(ntGetStdHandleFn, _NT_STD_OUTPUT_HANDLE, 0, 0, 0, 0, 0)
	ntStderr = ntcall(ntGetStdHandleFn, _NT_STD_ERROR_HANDLE, 0, 0, 0, 0, 0)
}

// ntwrite1 is the NT leg of runtime·write1, reached through
// ntwrite1tramp. fds 1 and 2 map straight to the cached std handles
// (no fd table in wave 1); anything else is EBADF. Returns the byte
// count or a negative errno, matching the write1 convention. write1
// runs during panics, but always with a valid g once boot completes,
// so routing through ntcall/asmcgocall is safe (pre-boot printing is
// out of scope for wave 1).
//
//go:nosplit
func ntwrite1(fd uintptr, p unsafe.Pointer, n int32) int32 {
	var h uintptr
	switch fd {
	case 1:
		h = ntStdout
	case 2:
		h = ntStderr
	default:
		return -9 // EBADF
	}
	var written uint32
	ok := ntcall(ntWriteFileFn, h, uintptr(p), uintptr(n), uintptr(unsafe.Pointer(&written)), 0, 0)
	if ok == 0 {
		return -5 // EIO
	}
	return int32(written)
}

// ntFutexsleep implements futexsleep over WaitOnAddress: compare
// *addr against a stack copy of val and wait with a millisecond
// timeout (round up; any positive ns waits at least 1ms). Spurious
// wakeups are allowed by the futexsleep contract, so the return value
// (including timeout) is ignored.
//
//go:nosplit
func ntFutexsleep(addr *uint32, val uint32, ns int64) {
	v := val
	ms := uintptr(_NT_INFINITE)
	if ns >= 0 {
		ms = uintptr((ns + 1e6 - 1) / 1e6)
	}
	ntcall(ntWaitOnAddressFn, uintptr(unsafe.Pointer(addr)), uintptr(unsafe.Pointer(&v)), 4, ms, 0, 0)
}

// ntFutexwakeup implements futexwakeup. Every futexwakeup caller
// passes cnt==1 (exhaustive grep, see DEBUGGING.md wave-1 design), so
// WakeByAddressSingle suffices; WakeByAddress* returns void, nothing
// to check.
//
//go:nosplit
func ntFutexwakeup(addr *uint32) {
	ntcall(ntWakeByAddressSingleFn, uintptr(unsafe.Pointer(addr)), 0, 0, 0, 0, 0)
}

// ntNewosproc is the NT leg of newosproc: CreateThread with a small
// (64KiB, reservation-only) NT stack; tstart_cosmo_nt pivots onto
// mp.g0's Go-allocated stack, so the Linux bookkeeping (mexit frees
// the g0 stack) is preserved and the NT stack dies with the thread.
// Same design as real cosmo's CloneWindows. The returned thread
// handle is leaked in wave 1 (cosmo stores it in the TIB; revisit
// with the thread-teardown wave).
//
// May run with m.p==nil, so write barriers are not allowed.
//
//go:nowritebarrier
func ntNewosproc(mp *m) {
	ret := ntcall(ntCreateThreadFn,
		0,                                     // lpThreadAttributes
		0x10000,                               // dwStackSize (64KiB)
		abi.FuncPCABI0(tstart_cosmo_nt),       // lpStartAddress
		uintptr(unsafe.Pointer(mp)),           // lpParameter
		_NT_STACK_SIZE_PARAM_IS_A_RESERVATION, // dwCreationFlags
		0)                                     // lpThreadId
	if ret == 0 {
		print("runtime: failed to create new OS thread (have ", mcount(), " already)\n")
		throw("newosproc")
	}
}

// ntSystemInfo is the win64 SYSTEM_INFO layout (48 bytes).
type ntSystemInfo struct {
	oemID                 uint32
	pageSize              uint32
	minAppAddr            uintptr
	maxAppAddr            uintptr
	activeProcessorMask   uintptr
	numberOfProcessors    uint32
	processorType         uint32
	allocationGranularity uint32
	processorLevel        uint16
	processorRevision     uint16
}

// ntNumCPU is the NT leg of getCPUCount.
func ntNumCPU() int32 {
	var si ntSystemInfo
	ntcall(ntGetSystemInfoFn, uintptr(unsafe.Pointer(&si)), 0, 0, 0, 0, 0)
	if n := int32(si.numberOfProcessors); n >= 1 {
		return n
	}
	return 1
}

// Memory primitives. VirtualAlloc/VirtualFree return 0 on failure.

//go:nosplit
func ntVirtualAlloc(v unsafe.Pointer, n uintptr, allocType, prot uintptr) unsafe.Pointer {
	return unsafe.Pointer(ntcall(ntVirtualAllocFn, uintptr(v), n, allocType, prot, 0, 0))
}

//go:nosplit
func ntVirtualFree(v unsafe.Pointer, n uintptr, freeType uintptr) uintptr {
	return ntcall(ntVirtualFreeFn, uintptr(v), n, freeType, 0, 0, 0)
}

// ntSyscallWrite and ntSyscallExit back the wave-1 WindowsFns hook
// table in internal/runtime/syscall/cosmo, covering user-level
// syscall.Write (fmt output) and syscall exit paths.

func ntSyscallWrite(fd int, p unsafe.Pointer, n int32) int32 {
	return ntwrite1(uintptr(fd), p, n)
}

func ntSyscallExit(code int32) {
	exit(code)
}

// ntSetSyscallFns installs the syscall-package hook table. Called from
// osArchInit on NT hosts, before any user code runs. The composite
// literal does not escape (SetWindowsFns copies it), so this is safe
// pre-mallocinit.
func ntSetSyscallFns() {
	cosmo.SetWindowsFns(&cosmo.WindowsFns{
		Write: ntSyscallWrite,
		Exit:  ntSyscallExit,
	})
}
