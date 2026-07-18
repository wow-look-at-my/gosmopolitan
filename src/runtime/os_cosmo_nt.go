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
	ntVirtualAllocFn           uintptr
	ntVirtualFreeFn            uintptr
	ntWriteFileFn              uintptr
	ntGetStdHandleFn           uintptr
	ntExitProcessFn            uintptr // asm: runtime·exit
	ntExitThreadFn             uintptr // asm: runtime·exitThread
	ntCreateThreadFn           uintptr
	ntSleepFn                  uintptr // asm: usleep, osyield
	ntGetSystemInfoFn          uintptr
	ntGetCommandLineWFn        uintptr
	ntGetEnvironmentStringsWFn uintptr
	ntWaitOnAddressFn          uintptr
	ntWakeByAddressSingleFn    uintptr

	// Wave 2 (file I/O, identity, console; all kernel32, all present
	// since forever - resolved with the same crash-poke discipline).
	ntGetLastErrorFn                 uintptr
	ntCloseHandleFn                  uintptr
	ntCreateFileWFn                  uintptr
	ntReadFileFn                     uintptr
	ntSetFilePointerExFn             uintptr
	ntSetEndOfFileFn                 uintptr
	ntFlushFileBuffersFn             uintptr
	ntGetFileInformationByHandleFn   uintptr
	ntGetFileInformationByHandleExFn uintptr
	ntDeleteFileWFn                  uintptr
	ntRemoveDirectoryWFn             uintptr
	ntMoveFileExWFn                  uintptr
	ntCreateDirectoryWFn             uintptr
	ntGetFileAttributesWFn           uintptr
	ntGetCurrentDirectoryWFn         uintptr
	ntSetCurrentDirectoryWFn         uintptr
	ntGetTempPathWFn                 uintptr
	ntGetModuleFileNameWFn           uintptr
	ntGetCurrentProcessIdFn          uintptr
	ntGetCurrentThreadIdFn           uintptr
	ntGetFileTypeFn                  uintptr
	ntGetConsoleModeFn               uintptr
	ntSetConsoleModeFn               uintptr
	ntSetConsoleOutputCPFn           uintptr
	ntSetConsoleCPFn                 uintptr

	// Chunk B (os/exec; all kernel32, present since forever).
	ntCreatePipeFn          uintptr
	ntDuplicateHandleFn     uintptr
	ntCreateProcessWFn      uintptr
	ntWaitForSingleObjectFn uintptr
	ntGetExitCodeProcessFn  uintptr
	ntGetProcessTimesFn     uintptr

	// Optional non-kernel32 imports: 0 when unavailable, and every
	// user degrades gracefully (the cosmo graceful-stub philosophy).
	ntQueryInformationProcessFn uintptr // ntdll: getppid
	ntProcessPrngFn             uintptr // bcryptprimitives ProcessPrng, or advapi32 SystemFunction036 (same signature)

	// Cached std handles (GetStdHandle(-10)/(-11)/(-12)).
	ntStdin  uintptr
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
	ntNameGetCommandLine = []byte("GetCommandLineW\x00")
	ntNameGetEnvStringsW = []byte("GetEnvironmentStringsW\x00")
	ntNameWaitOnAddress  = []byte("WaitOnAddress\x00")
	ntNameWakeByAddrSing = []byte("WakeByAddressSingle\x00")

	// Wave 2.
	ntNameGetLastError      = []byte("GetLastError\x00")
	ntNameCloseHandle       = []byte("CloseHandle\x00")
	ntNameCreateFileW       = []byte("CreateFileW\x00")
	ntNameReadFile          = []byte("ReadFile\x00")
	ntNameSetFilePointerEx  = []byte("SetFilePointerEx\x00")
	ntNameSetEndOfFile      = []byte("SetEndOfFile\x00")
	ntNameFlushFileBuffers  = []byte("FlushFileBuffers\x00")
	ntNameGetFileInfoByH    = []byte("GetFileInformationByHandle\x00")
	ntNameGetFileInfoByHEx  = []byte("GetFileInformationByHandleEx\x00")
	ntNameDeleteFileW       = []byte("DeleteFileW\x00")
	ntNameRemoveDirectoryW  = []byte("RemoveDirectoryW\x00")
	ntNameMoveFileExW       = []byte("MoveFileExW\x00")
	ntNameCreateDirectoryW  = []byte("CreateDirectoryW\x00")
	ntNameGetFileAttrsW     = []byte("GetFileAttributesW\x00")
	ntNameGetCurrentDirW    = []byte("GetCurrentDirectoryW\x00")
	ntNameSetCurrentDirW    = []byte("SetCurrentDirectoryW\x00")
	ntNameGetTempPathW      = []byte("GetTempPathW\x00")
	ntNameGetModuleFileW    = []byte("GetModuleFileNameW\x00")
	ntNameGetCurrentProcId  = []byte("GetCurrentProcessId\x00")
	ntNameGetCurrentThrId   = []byte("GetCurrentThreadId\x00")
	ntNameGetFileType       = []byte("GetFileType\x00")
	ntNameGetConsoleMode    = []byte("GetConsoleMode\x00")
	ntNameSetConsoleMode    = []byte("SetConsoleMode\x00")
	ntNameSetConsoleOutCP   = []byte("SetConsoleOutputCP\x00")
	ntNameSetConsoleCP      = []byte("SetConsoleCP\x00")
	ntNameCreatePipe        = []byte("CreatePipe\x00")
	ntNameDuplicateHandle   = []byte("DuplicateHandle\x00")
	ntNameCreateProcessW    = []byte("CreateProcessW\x00")
	ntNameWaitForSingleObj  = []byte("WaitForSingleObject\x00")
	ntNameGetExitCodeProc   = []byte("GetExitCodeProcess\x00")
	ntNameGetProcessTimes   = []byte("GetProcessTimes\x00")
	ntNameNtdll             = []byte("ntdll.dll\x00")
	ntNameNtQueryInfoProc   = []byte("NtQueryInformationProcess\x00")
	ntNameBcryptPrimitives  = []byte("bcryptprimitives.dll\x00")
	ntNameProcessPrng       = []byte("ProcessPrng\x00")
	ntNameAdvapi32          = []byte("advapi32.dll\x00")
	ntNameSystemFunction036 = []byte("SystemFunction036\x00") // RtlGenRandom
)

// Win32 constants used by wave 1 that only amd64 code references (the
// memory constants live in os_cosmo.go because mem_cosmo.go is shared
// with arm64).
const (
	_NT_STACK_SIZE_PARAM_IS_A_RESERVATION = 0x10000

	_NT_INFINITE = 0xFFFFFFFF

	_NT_STD_INPUT_HANDLE  = 0xFFFFFFF6 // (DWORD)-10, zero-extended
	_NT_STD_OUTPUT_HANDLE = 0xFFFFFFF5 // (DWORD)-11, zero-extended
	_NT_STD_ERROR_HANDLE  = 0xFFFFFFF4 // (DWORD)-12, zero-extended
)

// ntcallArgs is the argument block ntcall packs for the ntcall6
// trampoline. Field offsets are exported to assembly via go_asm.h.
// Kept at six arguments on purpose: ntcall sits inside the tightest
// nosplit chain in the port (cgoSigtramp -> ... -> write1 -> ntwrite1
// -> ntcall -> asmcgocall), which cannot afford a bigger frame. The
// syscall-emulation layer's wider calls (CreateFileW, CreateProcessW)
// use the separate ntcallArgs10/ntcall10 pair, which never appears in
// that chain.
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

// ntcallArgs10 is the ten-argument block for the ntcall10 trampoline
// (born as ntcallArgs8 in chunk A for 7-argument CreateFileW; widened
// - per the chunk-A rule that this parallel block is the one that
// grows - to ten for CreateProcessW in chunk B).
type ntcallArgs10 struct {
	fn  uintptr
	a1  uintptr
	a2  uintptr
	a3  uintptr
	a4  uintptr
	a5  uintptr
	a6  uintptr
	a7  uintptr
	a8  uintptr
	a9  uintptr
	a10 uintptr
	ret uintptr
}

// Implemented in sys_cosmo_nt_amd64.s.
func ntcall6()
func ntcall10()
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

// ntcall7 is ntcall for seven-argument functions (CreateFileW), via
// the wider ntcall10 trampoline.
//
//go:nosplit
func ntcall7(fn, a1, a2, a3, a4, a5, a6, a7 uintptr) uintptr {
	args := ntcallArgs10{fn: fn, a1: a1, a2: a2, a3: a3, a4: a4, a5: a5, a6: a6, a7: a7}
	asmcgocall(unsafe.Pointer(abi.FuncPCABI0(ntcall10)), unsafe.Pointer(&args))
	return args.ret
}

// ntcall10x is ntcall for ten-argument functions (CreateProcessW).
//
//go:nosplit
func ntcall10x(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10 uintptr) uintptr {
	args := ntcallArgs10{fn: fn, a1: a1, a2: a2, a3: a3, a4: a4, a5: a5,
		a6: a6, a7: a7, a8: a8, a9: a9, a10: a10}
	asmcgocall(unsafe.Pointer(abi.FuncPCABI0(ntcall10)), unsafe.Pointer(&args))
	return args.ret
}

// ntcallE ("with error") performs ntcall7 and returns the thread's
// GetLastError alongside the result. The two Win32 calls stay on one
// thread: this function lives in package runtime, so it is never
// asynchronously preempted (runtime frames are not async-preemption
// safe points), and there is no preemptible prologue between the two
// nosplit ntcall invocations - the thread-local last-error value
// cannot be lost to a migration. Callers pass pointers as
// uintptr(unsafe.Pointer(x)) directly in the argument list; nosplit
// (plus liveness at the call site or an explicit KeepAlive) keeps the
// pointee valid and unmoved for the duration.
//
//go:nosplit
func ntcallE(fn, a1, a2, a3, a4, a5, a6, a7 uintptr) (r, lastErr uintptr) {
	r = ntcall7(fn, a1, a2, a3, a4, a5, a6, a7)
	lastErr = ntcall(ntGetLastErrorFn, 0, 0, 0, 0, 0, 0)
	return
}

// ntcallSE ("syscall-state, with error") is ntcallE bracketed by
// entersyscall/exitsyscall, for Win32 calls that can block
// indefinitely (ReadFile/WriteFile on consoles and pipes,
// FlushFileBuffers): the P can be retaken by sysmon while the thread
// is parked in the kernel, exactly like a real blocking syscall.
// Must only be used from user-goroutine context - the
// syscall-emulation layer - never from boot, g0, or runtime-internal
// paths. The g stays bound to this M between entersyscall and
// exitsyscall, so the GetLastError fetch still reads the right
// thread's error.
//
//go:nosplit
func ntcallSE(fn, a1, a2, a3, a4, a5, a6, a7 uintptr) (r, lastErr uintptr) {
	entersyscall()
	r = ntcall7(fn, a1, a2, a3, a4, a5, a6, a7)
	lastErr = ntcall(ntGetLastErrorFn, 0, 0, 0, 0, 0, 0)
	exitsyscall()
	return
}

// ntcallSE10 is ntcallSE for ten-argument functions (CreateProcessW,
// which can block on image load). Same contract as ntcallSE:
// user-goroutine context only.
//
//go:nosplit
func ntcallSE10(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10 uintptr) (r, lastErr uintptr) {
	entersyscall()
	r = ntcall10x(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10)
	lastErr = ntcall(ntGetLastErrorFn, 0, 0, 0, 0, 0, 0)
	exitsyscall()
	return
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
	ntGetCommandLineWFn = k32sym(&ntNameGetCommandLine[0])
	ntGetEnvironmentStringsWFn = k32sym(&ntNameGetEnvStringsW[0])

	// Wave 2: file I/O, identity, console (all kernel32).
	ntGetLastErrorFn = k32sym(&ntNameGetLastError[0])
	ntCloseHandleFn = k32sym(&ntNameCloseHandle[0])
	ntCreateFileWFn = k32sym(&ntNameCreateFileW[0])
	ntReadFileFn = k32sym(&ntNameReadFile[0])
	ntSetFilePointerExFn = k32sym(&ntNameSetFilePointerEx[0])
	ntSetEndOfFileFn = k32sym(&ntNameSetEndOfFile[0])
	ntFlushFileBuffersFn = k32sym(&ntNameFlushFileBuffers[0])
	ntGetFileInformationByHandleFn = k32sym(&ntNameGetFileInfoByH[0])
	ntGetFileInformationByHandleExFn = k32sym(&ntNameGetFileInfoByHEx[0])
	ntDeleteFileWFn = k32sym(&ntNameDeleteFileW[0])
	ntRemoveDirectoryWFn = k32sym(&ntNameRemoveDirectoryW[0])
	ntMoveFileExWFn = k32sym(&ntNameMoveFileExW[0])
	ntCreateDirectoryWFn = k32sym(&ntNameCreateDirectoryW[0])
	ntGetFileAttributesWFn = k32sym(&ntNameGetFileAttrsW[0])
	ntGetCurrentDirectoryWFn = k32sym(&ntNameGetCurrentDirW[0])
	ntSetCurrentDirectoryWFn = k32sym(&ntNameSetCurrentDirW[0])
	ntGetTempPathWFn = k32sym(&ntNameGetTempPathW[0])
	ntGetModuleFileNameWFn = k32sym(&ntNameGetModuleFileW[0])
	ntGetCurrentProcessIdFn = k32sym(&ntNameGetCurrentProcId[0])
	ntGetCurrentThreadIdFn = k32sym(&ntNameGetCurrentThrId[0])
	ntGetFileTypeFn = k32sym(&ntNameGetFileType[0])
	ntGetConsoleModeFn = k32sym(&ntNameGetConsoleMode[0])
	ntSetConsoleModeFn = k32sym(&ntNameSetConsoleMode[0])
	ntSetConsoleOutputCPFn = k32sym(&ntNameSetConsoleOutCP[0])
	ntSetConsoleCPFn = k32sym(&ntNameSetConsoleCP[0])

	// Chunk B: os/exec (all kernel32).
	ntCreatePipeFn = k32sym(&ntNameCreatePipe[0])
	ntDuplicateHandleFn = k32sym(&ntNameDuplicateHandle[0])
	ntCreateProcessWFn = k32sym(&ntNameCreateProcessW[0])
	ntWaitForSingleObjectFn = k32sym(&ntNameWaitForSingleObj[0])
	ntGetExitCodeProcessFn = k32sym(&ntNameGetExitCodeProc[0])
	ntGetProcessTimesFn = k32sym(&ntNameGetProcessTimes[0])

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

	// Optional imports, resolved gracefully (a 0 pointer degrades to
	// ENOSYS or a fallback at the use site, never a boot crash):
	// getppid needs ntdll's NtQueryInformationProcess; entropy wants
	// bcryptprimitives' ProcessPrng (what upstream Go uses on Windows
	// since 1.22), falling back to advapi32's SystemFunction036
	// (RtlGenRandom), which has the same (buf, len) signature.
	if ntdll := ntcall(lla, uintptr(unsafe.Pointer(&ntNameNtdll[0])), 0, 0, 0, 0, 0); ntdll != 0 {
		ntQueryInformationProcessFn = ntcall(gpa, ntdll, uintptr(unsafe.Pointer(&ntNameNtQueryInfoProc[0])), 0, 0, 0, 0)
	}
	if bp := ntcall(lla, uintptr(unsafe.Pointer(&ntNameBcryptPrimitives[0])), 0, 0, 0, 0, 0); bp != 0 {
		ntProcessPrngFn = ntcall(gpa, bp, uintptr(unsafe.Pointer(&ntNameProcessPrng[0])), 0, 0, 0, 0)
	}
	if ntProcessPrngFn == 0 {
		if adv := ntcall(lla, uintptr(unsafe.Pointer(&ntNameAdvapi32[0])), 0, 0, 0, 0, 0); adv != 0 {
			ntProcessPrngFn = ntcall(gpa, adv, uintptr(unsafe.Pointer(&ntNameSystemFunction036[0])), 0, 0, 0, 0)
		}
	}

	ntStdin = ntcall(ntGetStdHandleFn, _NT_STD_INPUT_HANDLE, 0, 0, 0, 0, 0)
	ntStdout = ntcall(ntGetStdHandleFn, _NT_STD_OUTPUT_HANDLE, 0, 0, 0, 0, 0)
	ntStderr = ntcall(ntGetStdHandleFn, _NT_STD_ERROR_HANDLE, 0, 0, 0, 0, 0)
}

// ntwrite1 is the NT leg of runtime·write1, reached through
// ntwrite1tramp. fds 1 and 2 map straight to the cached std handles -
// deliberately NOT through the wave-2 fd table: write1 is the panic
// and runtime-print path, and the runtime only ever writes to 1/2, so
// the fewer moving parts the better. (User-level syscall.Write goes
// through the table, os_cosmo_nt_sys.go.) Anything else is EBADF.
// Returns the byte count or a negative errno, matching the write1
// convention. write1 runs during panics, but always with a valid g
// once boot completes, so routing through ntcall/asmcgocall is safe
// (pre-boot printing is out of scope).
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

// ntSetSyscallFns installs the syscall-package hook table. Called from
// osArchInit on NT hosts, before any user code runs. The composite
// literal does not escape (SetWindowsFns copies it), so this is safe
// pre-mallocinit. The dispatcher lives in os_cosmo_nt_sys.go.
func ntSetSyscallFns() {
	cosmo.SetWindowsFns(&cosmo.WindowsFns{
		Emulate: ntSyscallEmulate,
		Spawn:   ntSpawn,
	})
}

// Command line and environment. The NT boot stub fabricates a
// one-entry argv and an empty envp (rt0_cosmo_nt_amd64.s); the real
// values come from GetCommandLineW/GetEnvironmentStringsW in the
// goargs/goenvs NT branches below. Both run inside schedinit AFTER
// mallocinit (proc.go: mallocinit ... goargs; goenvs), so ordinary
// allocation is fine here.

// ntUTF16ToString converts a UTF-16 sequence to a Go string, combining
// surrogate pairs into their code points (which runtime.gostringw does
// not). Unpaired surrogates become U+FFFD, matching unicode/utf16.
func ntUTF16ToString(s []uint16) string {
	buf := make([]byte, 0, len(s)) // exact for ASCII; append grows the rest
	var tmp [4]byte
	for i := 0; i < len(s); i++ {
		var r rune
		switch c := s[i]; {
		case c < 0xd800 || c >= 0xe000:
			r = rune(c)
		case c < 0xdc00 && i+1 < len(s) && s[i+1] >= 0xdc00 && s[i+1] < 0xe000:
			r = 0x10000 + (rune(c)-0xd800)<<10 + (rune(s[i+1]) - 0xdc00)
			i++
		default:
			r = 0xfffd // unpaired surrogate
		}
		n := encoderune(tmp[:], r)
		buf = append(buf, tmp[:n]...)
	}
	return string(buf)
}

// ntCommandLineToArgv splits a Windows command line into arguments
// following the conventions documented at
// http://daviddeley.com/autohotkey/parameters/parameters.htm#WINARGV.
// It is a port of commandLineToArgv/readNextArg/appendBSBytes from
// os/exec_windows.go: package os only runs that parse when
// GOOS == "windows"; under GOOS=cosmo os.Args comes from the
// runtime's argslice, so the parse has to happen here.
func ntCommandLineToArgv(cmd string) []string {
	var args []string
	for len(cmd) > 0 {
		if cmd[0] == ' ' || cmd[0] == '\t' {
			cmd = cmd[1:]
			continue
		}
		var arg []byte
		arg, cmd = ntReadNextArg(cmd)
		args = append(args, string(arg))
	}
	return args
}

// ntAppendBS appends n '\\' bytes to b and returns the resulting slice.
func ntAppendBS(b []byte, n int) []byte {
	for ; n > 0; n-- {
		b = append(b, '\\')
	}
	return b
}

// ntReadNextArg splits command line string cmd into next argument and
// command line remainder. (See ntCommandLineToArgv for provenance.)
func ntReadNextArg(cmd string) (arg []byte, rest string) {
	var b []byte
	var inquote bool
	var nslash int
	for ; len(cmd) > 0; cmd = cmd[1:] {
		c := cmd[0]
		switch c {
		case ' ', '\t':
			if !inquote {
				return ntAppendBS(b, nslash), cmd[1:]
			}
		case '"':
			b = ntAppendBS(b, nslash/2)
			if nslash%2 == 0 {
				// use "Prior to 2008" rule from
				// http://daviddeley.com/autohotkey/parameters/parameters.htm
				// section 5.2 to deal with double double quotes
				if inquote && len(cmd) > 1 && cmd[1] == '"' {
					b = append(b, c)
					cmd = cmd[1:]
				}
				inquote = !inquote
			} else {
				b = append(b, c)
			}
			nslash = 0
			continue
		case '\\':
			nslash++
			continue
		}
		b = ntAppendBS(b, nslash)
		nslash = 0
		b = append(b, c)
	}
	return ntAppendBS(b, nslash), ""
}

// cosmoNTGoargs is goargs's NT branch (runtime1.go): build argslice by
// parsing GetCommandLineW instead of reading the boot block, whose
// fabricated argv is the single static "APE". Returns false on non-NT
// hosts - and on an empty command line, keeping the fabricated argv as
// the fallback.
func cosmoNTGoargs() bool {
	if !iswindows() {
		return false
	}
	cmd := (*uint16)(unsafe.Pointer(ntcall(ntGetCommandLineWFn, 0, 0, 0, 0, 0, 0)))
	if cmd == nil {
		return false
	}
	n := findnullw(cmd)
	if n == 0 {
		return false
	}
	args := ntCommandLineToArgv(ntUTF16ToString(unsafe.Slice(cmd, n)))
	if len(args) == 0 {
		return false
	}
	argslice = args
	return true
}

// ntGoenvs is goenvs's NT branch (os_cosmo.go): decode the
// double-NUL-terminated UTF-16 block from GetEnvironmentStringsW
// ("A=B\x00C=D\x00\x00") into envs, the same shape goenvs_unix
// produces; upstream os_windows.go goenvs is the model. The block is
// deliberately not released: FreeEnvironmentStringsW is not in the
// wave-1 resolve set and the one-shot boot leak is harmless.
func ntGoenvs() {
	block := unsafe.Pointer(ntcall(ntGetEnvironmentStringsWFn, 0, 0, 0, 0, 0, 0))
	if block == nil {
		envs = make([]string, 0)
		return
	}
	p := (*[1 << 24]uint16)(block)
	n := 0
	for from, i := 0, 0; ; i++ {
		if p[i] == 0 {
			// An empty string marks the end of the block.
			if i == from {
				break
			}
			from = i + 1
			n++
		}
	}
	envs = make([]string, n)
	off := 0
	for i := range envs {
		start := off
		for p[off] != 0 {
			off++
		}
		envs[i] = ntUTF16ToString(p[start:off])
		off++ // skip the NUL
	}
}
