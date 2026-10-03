// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// Windows NT personality.
//
// Everything here is gated on iswindows() (__hostos == _HOSTWINDOWS).
// Windows/arm64 has no APE boot stub, so nothing here is reachable on
// arm64: iswindows() can never be true there.
//
// A win64 function is reached through runtime·ntcall6, a host-ABI
// trampoline invoked via asmcgocall, so the g0 stack switch and the
// stack accounting come for free. The function-pointer table resolves
// at osArchInit from both loader-filled IAT slots, GetProcAddress and
// LoadLibraryA, which mirrors the darwin port's dlsym idiom.

package runtime

import (
	"internal/abi"
	"internal/runtime/atomic"
	"unsafe"
)

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
	ntExitProcessFn            uintptr // ntExit (Go) + ntExitEncoded (asm)
	ntExitThreadFn             uintptr // asm: runtime·exitThread
	ntCreateThreadFn           uintptr
	ntSleepFn                  uintptr // asm: usleep, osyield
	ntGetSystemInfoFn          uintptr
	ntGetCommandLineWFn        uintptr
	ntGetEnvironmentStringsWFn uintptr
	ntWaitOnAddressFn          uintptr
	ntWakeByAddressSingleFn    uintptr

	ntGetLastErrorFn     uintptr
	ntCloseHandleFn      uintptr
	ntCreateFileWFn      uintptr
	ntReadFileFn         uintptr
	ntSetFilePointerExFn uintptr
	ntSetEndOfFileFn     uintptr
	ntFlushFileBuffersFn uintptr
	// QueryPerformanceCounter. nanotime reads KUSER_SHARED_DATA, which moves once a timer tick.
	ntQueryPerfCounterFn uintptr
	// The section API behind mmap (os_cosmo_nt_mmap.go). Optional: a zero answers ENOSYS where it is called.
	ntCreateFileMappingWFn uintptr
	ntMapViewOfFileFn      uintptr
	ntUnmapViewOfFileFn    uintptr
	ntFlushViewOfFileFn    uintptr
	ntVirtualQueryFn       uintptr
	ntVirtualLockFn        uintptr
	ntVirtualUnlockFn      uintptr
	// The flock(2) pair.
	ntLockFileExFn   uintptr
	ntUnlockFileExFn uintptr
	// uname's sources (ntEmuUname). Both optional.
	ntRtlGetVersionFn    uintptr
	ntGetComputerNameWFn uintptr
	// The host machine, for GOARCH (hostarch_cosmo.go).
	ntIsWow64Process2Fn uintptr
	// statfs/fstatfs (os_cosmo_nt_statfs.go). All optional.
	ntGetVolumePathNameWFn    uintptr
	ntGetDiskFreeSpaceWFn     uintptr
	ntGetDiskFreeSpaceExWFn   uintptr
	ntGetVolumeInformationWFn uintptr
	ntSetFileTimeFn                  uintptr
	ntGetSystemTimeAsFileTimeFn      uintptr
	ntGetFinalPathNameByHandleWFn    uintptr
	ntCreateHardLinkWFn              uintptr
	ntSetFileAttributesWFn           uintptr
	ntCreateSymbolicLinkWFn          uintptr
	ntDeviceIoControlFn              uintptr
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
	ntCancelIoExFn          uintptr
	ntDuplicateHandleFn     uintptr
	ntCreateProcessWFn      uintptr
	ntWaitForSingleObjectFn uintptr
	ntGetExitCodeProcessFn  uintptr
	ntGetProcessTimesFn     uintptr
	ntGetThreadTimesFn      uintptr
	ntGetProcessMemInfoFn   uintptr

	// Chunk D (signals/VEH/preemption; all kernel32, present since forever except the optional WER pair).
	ntAddVectoredExceptionHandlerFn uintptr
	ntAddVectoredContinueHandlerFn  uintptr
	ntSetErrorModeFn                uintptr
	ntGetErrorModeFn                uintptr
	ntSuspendThreadFn               uintptr
	ntResumeThreadFn                uintptr
	ntGetThreadContextFn            uintptr
	ntSetThreadContextFn            uintptr
	ntSetConsoleCtrlHandlerFn       uintptr
	ntCreateEventWFn                uintptr
	ntSetEventFn                    uintptr
	ntTerminateProcessFn            uintptr
	ntWerGetFlagsFn                 uintptr // optional (missing on old wine)
	ntWerSetFlagsFn                 uintptr // optional

	ntOpenProcessFn uintptr

	ntCreateWaitableTimerExWFn uintptr
	ntCreateWaitableTimerWFn   uintptr
	ntSetWaitableTimerFn       uintptr
	ntSetThreadPriorityFn      uintptr // optional (best-effort use)

	ntGenerateConsoleCtrlEventFn uintptr

	ntQueryInformationProcessFn uintptr // ntdll: getppid
	ntProcessPrngFn             uintptr // bcryptprimitives ProcessPrng, or advapi32 SystemFunction036 (same signature)

	ntStdin  uintptr
	ntStdout uintptr
	ntStderr uintptr
)

// C string constants for resolution.
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

	ntNameGetLastError      = []byte("GetLastError\x00")
	ntNameCloseHandle       = []byte("CloseHandle\x00")
	ntNameCreateFileW       = []byte("CreateFileW\x00")
	ntNameReadFile          = []byte("ReadFile\x00")
	ntNameSetFilePointerEx  = []byte("SetFilePointerEx\x00")
	ntNameSetEndOfFile      = []byte("SetEndOfFile\x00")
	ntNameFlushFileBuffers  = []byte("FlushFileBuffers\x00")
	ntNameQueryPerfCounter  = []byte("QueryPerformanceCounter\x00")
	ntNameCreateFileMapping = []byte("CreateFileMappingW\x00")
	ntNameMapViewOfFile     = []byte("MapViewOfFile\x00")
	ntNameUnmapViewOfFile   = []byte("UnmapViewOfFile\x00")
	ntNameFlushViewOfFile   = []byte("FlushViewOfFile\x00")
	ntNameVirtualQuery      = []byte("VirtualQuery\x00")
	ntNameVirtualLock       = []byte("VirtualLock\x00")
	ntNameVirtualUnlock     = []byte("VirtualUnlock\x00")
	ntNameRtlGetVersion     = []byte("RtlGetVersion\x00")
	ntNameGetComputerNameW  = []byte("GetComputerNameW\x00")
	ntNameLockFileEx        = []byte("LockFileEx\x00")
	ntNameIsWow64Process2   = []byte("IsWow64Process2\x00")
	ntNameGetVolumePathW    = []byte("GetVolumePathNameW\x00")
	ntNameGetDiskFreeSpaceW = []byte("GetDiskFreeSpaceW\x00")
	ntNameGetDiskFreeSpcExW = []byte("GetDiskFreeSpaceExW\x00")
	ntNameGetVolumeInfoW    = []byte("GetVolumeInformationW\x00")
	ntNameUnlockFileEx      = []byte("UnlockFileEx\x00")
	ntNameSetFileTime       = []byte("SetFileTime\x00")
	ntNameGetSysTimeAsFt    = []byte("GetSystemTimeAsFileTime\x00")
	ntNameGetFinalPathW     = []byte("GetFinalPathNameByHandleW\x00")
	ntNameCreateHardLinkW   = []byte("CreateHardLinkW\x00")
	ntNameSetFileAttrsW     = []byte("SetFileAttributesW\x00")
	ntNameCreateSymlinkW    = []byte("CreateSymbolicLinkW\x00")
	ntNameDeviceIoControl   = []byte("DeviceIoControl\x00")
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
	ntNameCancelIoEx        = []byte("CancelIoEx\x00")
	ntNameDuplicateHandle   = []byte("DuplicateHandle\x00")
	ntNameCreateProcessW    = []byte("CreateProcessW\x00")
	ntNameWaitForSingleObj  = []byte("WaitForSingleObject\x00")
	ntNameGetExitCodeProc   = []byte("GetExitCodeProcess\x00")
	ntNameGetProcessTimes   = []byte("GetProcessTimes\x00")
	ntNameGetThreadTimes    = []byte("GetThreadTimes\x00")
	ntNameGetProcessMemInfo = []byte("K32GetProcessMemoryInfo\x00")
	ntNameAddVEH            = []byte("AddVectoredExceptionHandler\x00")
	ntNameAddVCH            = []byte("AddVectoredContinueHandler\x00")
	ntNameSetErrorMode      = []byte("SetErrorMode\x00")
	ntNameGetErrorMode      = []byte("GetErrorMode\x00")
	ntNameSuspendThread     = []byte("SuspendThread\x00")
	ntNameResumeThread      = []byte("ResumeThread\x00")
	ntNameGetThreadContext  = []byte("GetThreadContext\x00")
	ntNameSetThreadContext  = []byte("SetThreadContext\x00")
	ntNameSetConsoleCtrlH   = []byte("SetConsoleCtrlHandler\x00")
	ntNameCreateEventW      = []byte("CreateEventW\x00")
	ntNameSetEvent          = []byte("SetEvent\x00")
	ntNameTerminateProcess  = []byte("TerminateProcess\x00")
	ntNameWerGetFlags       = []byte("WerGetFlags\x00")
	ntNameWerSetFlags       = []byte("WerSetFlags\x00")
	ntNameOpenProcess       = []byte("OpenProcess\x00")
	ntNameCreateWTimerExW   = []byte("CreateWaitableTimerExW\x00")
	ntNameCreateWTimerW     = []byte("CreateWaitableTimerW\x00")
	ntNameSetWaitableTimer  = []byte("SetWaitableTimer\x00")
	ntNameSetThreadPriority = []byte("SetThreadPriority\x00")
	ntNameGenConsoleCtrlEvt = []byte("GenerateConsoleCtrlEvent\x00")
	ntNameNtdll             = []byte("ntdll.dll\x00")
	ntNameNtQueryInfoProc   = []byte("NtQueryInformationProcess\x00")
	ntNameBcryptPrimitives  = []byte("bcryptprimitives.dll\x00")
	ntNameProcessPrng       = []byte("ProcessPrng\x00")
	ntNameAdvapi32          = []byte("advapi32.dll\x00")
	ntNameSystemFunction036 = []byte("SystemFunction036\x00") // RtlGenRandom
)

const (
	_NT_STACK_SIZE_PARAM_IS_A_RESERVATION = 0x10000

	_NT_INFINITE = 0xFFFFFFFF

	// GetCurrentProcess() pseudo-handle, and the DuplicateHandle option every caller of it uses.
	_NT_CURRENT_PROCESS       = ^uintptr(0)
	_NT_DUPLICATE_SAME_ACCESS = 0x2

	_NT_STD_INPUT_HANDLE  = 0xFFFFFFF6
	_NT_STD_OUTPUT_HANDLE = 0xFFFFFFF5
	_NT_STD_ERROR_HANDLE  = 0xFFFFFFF4
)

// ntcallArgs is the argument block ntcall packs for the ntcall6 trampoline.
// Field offsets are exported to assembly via go_asm.h.
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

// ntcallArgs10 is those-argument block for the ntcall10 trampoline (born as
// ntcallArgs8 in chunk A for 7-argument CreateFileW.
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

// Implemented in sys_cosmo_nt_<goarch>.s.
func ntcall6()
func ntcall10()
func tstart_cosmo_nt()
func ntwrite1tramp(fd uintptr, p unsafe.Pointer, n int32) int32

// ntcall calls the win64 function fn with up to integer arguments through the
// ntcall6 trampoline via asmcgocall.
//
//go:nosplit
func ntcall(fn, a1, a2, a3, a4, a5, a6 uintptr) uintptr {
	args := ntcallArgs{fn: fn, a1: a1, a2: a2, a3: a3, a4: a4, a5: a5, a6: a6}
	asmcgocall(unsafe.Pointer(abi.FuncPCABI0(ntcall6)), unsafe.Pointer(&args))
	return args.ret
}

//
//go:nosplit
func ntcall7(fn, a1, a2, a3, a4, a5, a6, a7 uintptr) uintptr {
	args := ntcallArgs10{fn: fn, a1: a1, a2: a2, a3: a3, a4: a4, a5: a5, a6: a6, a7: a7}
	asmcgocall(unsafe.Pointer(abi.FuncPCABI0(ntcall10)), unsafe.Pointer(&args))
	return args.ret
}

//
//go:nosplit
func ntcall10x(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10 uintptr) uintptr {
	args := ntcallArgs10{fn: fn, a1: a1, a2: a2, a3: a3, a4: a4, a5: a5,
		a6: a6, a7: a7, a8: a8, a9: a9, a10: a10}
	asmcgocall(unsafe.Pointer(abi.FuncPCABI0(ntcall10)), unsafe.Pointer(&args))
	return args.ret
}

// ntcallE ("with error") performs ntcall7 and returns the thread's
// GetLastError alongside the result.
//
//go:nosplit
func ntcallE(fn, a1, a2, a3, a4, a5, a6, a7 uintptr) (r, lastErr uintptr) {
	r = ntcall7(fn, a1, a2, a3, a4, a5, a6, a7)
	lastErr = uintptr(getg().m.ntLastError)
	return
}

// ntHighPrecisionTicks reads QueryPerformanceCounter, and reports whether it
// answered. nanotime reads KUSER_SHARED_DATA's InterruptTime.
func ntHighPrecisionTicks() (int64, bool) {
	if !iswindows() || ntQueryPerfCounterFn == 0 {
		return 0, false
	}
	var ticks int64
	if ntcall7(ntQueryPerfCounterFn, uintptr(unsafe.Pointer(&ticks)), 0, 0, 0, 0, 0, 0) == 0 {
		return 0, false
	}
	return ticks, true
}

// ntcallSEcheck refuses a blocking call made under a runtime lock.
//
//go:nosplit
func ntcallSEcheck() {
	if getg().m.locks != 0 {
		throw("ntcallSE: runtime lock held across a blocking win64 call")
	}
}

// ntcallSE ("syscall-state, with error") is ntcallE bracketed by entersyscall
// and exitsyscall, for a Win32 call that can block indefinitely, so sysmon
// can retake the P while the thread parks in the kernel. Use it ONLY from
// user-goroutine context.
//
//go:nosplit
func ntcallSE(fn, a1, a2, a3, a4, a5, a6, a7 uintptr) (r, lastErr uintptr) {
	ntcallSEcheck()
	entersyscall()
	osPreemptExtEnter(getg().m)
	r = ntcall7(fn, a1, a2, a3, a4, a5, a6, a7)
	lastErr = uintptr(getg().m.ntLastError)
	osPreemptExtExit(getg().m)
	exitsyscall()
	return
}

// Same contract as ntcallSE: user-goroutine context only.
//
//go:nosplit
func ntcallSE10(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10 uintptr) (r, lastErr uintptr) {
	ntcallSEcheck()
	entersyscall()
	osPreemptExtEnter(getg().m)
	r = ntcall10x(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10)
	lastErr = uintptr(getg().m.ntLastError)
	osPreemptExtExit(getg().m)
	exitsyscall()
	return
}

// ntCrash stores code at address code, so the faulting address names
// the failure. Same idiom as the 0xf1 pokes in sys_cosmo_amd64.s.
//
//go:nosplit
func ntCrash(code uintptr) {
	// Say which first.
	var msg [40]byte
	copy(msg[:], "runtime: NT boot failed at 0x")
	n := 29
	for shift := 4; shift >= 0; shift -= 4 {
		d := byte(code>>uint(shift)) & 0xf
		if d < 10 {
			msg[n] = '0' + d
		} else {
			msg[n] = 'a' + d - 10
		}
		n++
	}
	msg[n] = '\n'
	ntwrite1(2, unsafe.Pointer(&msg[0]), int32(n+1))
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
	// The std handles are cached HERE, next to both calls that reach them, rather than at the end of this function.
	ntStdin = ntcall(ntGetStdHandleFn, _NT_STD_INPUT_HANDLE, 0, 0, 0, 0, 0)
	ntStdout = ntcall(ntGetStdHandleFn, _NT_STD_OUTPUT_HANDLE, 0, 0, 0, 0, 0)
	ntStderr = ntcall(ntGetStdHandleFn, _NT_STD_ERROR_HANDLE, 0, 0, 0, 0, 0)
	ntBoot("handles cached")
	ntExitProcessFn = k32sym(&ntNameExitProcess[0])
	ntExitThreadFn = k32sym(&ntNameExitThread[0])
	ntCreateThreadFn = k32sym(&ntNameCreateThread[0])
	ntSleepFn = k32sym(&ntNameSleep[0])
	ntGetSystemInfoFn = k32sym(&ntNameGetSystemInfo[0])
	ntGetCommandLineWFn = k32sym(&ntNameGetCommandLine[0])
	ntGetEnvironmentStringsWFn = k32sym(&ntNameGetEnvStringsW[0])

	ntGetLastErrorFn = k32sym(&ntNameGetLastError[0])
	ntCloseHandleFn = k32sym(&ntNameCloseHandle[0])
	ntCreateFileWFn = k32sym(&ntNameCreateFileW[0])
	ntReadFileFn = k32sym(&ntNameReadFile[0])
	ntSetFilePointerExFn = k32sym(&ntNameSetFilePointerEx[0])
	ntSetEndOfFileFn = k32sym(&ntNameSetEndOfFile[0])
	ntFlushFileBuffersFn = k32sym(&ntNameFlushFileBuffers[0])
	ntQueryPerfCounterFn = k32sym(&ntNameQueryPerfCounter[0])
	ntCreateFileMappingWFn = k32sym(&ntNameCreateFileMapping[0])
	ntMapViewOfFileFn = k32sym(&ntNameMapViewOfFile[0])
	ntUnmapViewOfFileFn = k32sym(&ntNameUnmapViewOfFile[0])
	ntFlushViewOfFileFn = k32sym(&ntNameFlushViewOfFile[0])
	ntVirtualQueryFn = k32sym(&ntNameVirtualQuery[0])
	ntVirtualLockFn = k32sym(&ntNameVirtualLock[0])
	ntVirtualUnlockFn = k32sym(&ntNameVirtualUnlock[0])
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
	ntCancelIoExFn = k32sym(&ntNameCancelIoEx[0])
	ntDuplicateHandleFn = k32sym(&ntNameDuplicateHandle[0])
	ntCreateProcessWFn = k32sym(&ntNameCreateProcessW[0])
	ntWaitForSingleObjectFn = k32sym(&ntNameWaitForSingleObj[0])
	ntGetExitCodeProcessFn = k32sym(&ntNameGetExitCodeProc[0])
	ntGetProcessTimesFn = k32sym(&ntNameGetProcessTimes[0])
	ntGetThreadTimesFn = k32sym(&ntNameGetThreadTimes[0])
	ntGetProcessMemInfoFn = k32sym(&ntNameGetProcessMemInfo[0])

	// Chunk D: signals/VEH/preemption (all kernel32.
	ntAddVectoredExceptionHandlerFn = k32sym(&ntNameAddVEH[0])
	ntAddVectoredContinueHandlerFn = k32sym(&ntNameAddVCH[0])
	ntSetErrorModeFn = k32sym(&ntNameSetErrorMode[0])
	ntGetErrorModeFn = k32sym(&ntNameGetErrorMode[0])
	ntSuspendThreadFn = k32sym(&ntNameSuspendThread[0])
	ntResumeThreadFn = k32sym(&ntNameResumeThread[0])
	ntGetThreadContextFn = k32sym(&ntNameGetThreadContext[0])
	ntSetThreadContextFn = k32sym(&ntNameSetThreadContext[0])
	ntSetConsoleCtrlHandlerFn = k32sym(&ntNameSetConsoleCtrlH[0])
	ntCreateEventWFn = k32sym(&ntNameCreateEventW[0])
	ntSetEventFn = k32sym(&ntNameSetEvent[0])
	ntTerminateProcessFn = k32sym(&ntNameTerminateProcess[0])
	ntWerGetFlagsFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameWerGetFlags[0])), 0, 0, 0, 0)
	ntWerSetFlagsFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameWerSetFlags[0])), 0, 0, 0, 0)

	ntOpenProcessFn = k32sym(&ntNameOpenProcess[0])

	ntCreateWaitableTimerWFn = k32sym(&ntNameCreateWTimerW[0])
	ntSetWaitableTimerFn = k32sym(&ntNameSetWaitableTimer[0])
	ntCreateWaitableTimerExWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameCreateWTimerExW[0])), 0, 0, 0, 0)
	ntSetThreadPriorityFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameSetThreadPriority[0])), 0, 0, 0, 0)

	ntGenerateConsoleCtrlEventFn = k32sym(&ntNameGenConsoleCtrlEvt[0])

	// WaitOnAddress and friends live in the api-ms-win-core-synch forwarder DLL (Win8+; real cosmo imports the same one).
	synch := ntcall(lla, uintptr(unsafe.Pointer(&ntNameSynchDLL[0])), 0, 0, 0, 0, 0)
	if synch == 0 {
		ntCrash(0xf4)
	}
	ntWaitOnAddressFn = ntcall(gpa, synch, uintptr(unsafe.Pointer(&ntNameWaitOnAddress[0])), 0, 0, 0, 0)
	ntWakeByAddressSingleFn = ntcall(gpa, synch, uintptr(unsafe.Pointer(&ntNameWakeByAddrSing[0])), 0, 0, 0, 0)
	if ntWaitOnAddressFn == 0 || ntWakeByAddressSingleFn == 0 {
		ntCrash(0xf5)
	}

	if ntdll := ntcall(lla, uintptr(unsafe.Pointer(&ntNameNtdll[0])), 0, 0, 0, 0, 0); ntdll != 0 {
		ntQueryInformationProcessFn = ntcall(gpa, ntdll, uintptr(unsafe.Pointer(&ntNameNtQueryInfoProc[0])), 0, 0, 0, 0)
		// uname's release and version (ntEmuUname).
		ntRtlGetVersionFn = ntcall(gpa, ntdll, uintptr(unsafe.Pointer(&ntNameRtlGetVersion[0])), 0, 0, 0, 0)
	}
	ntGetComputerNameWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameGetComputerNameW[0])), 0, 0, 0, 0)
	// The metadata wave (os_cosmo_nt_meta.go): utimensat, truncate, fchdir, linkat.
	ntSetFileTimeFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameSetFileTime[0])), 0, 0, 0, 0)
	ntGetSystemTimeAsFileTimeFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameGetSysTimeAsFt[0])), 0, 0, 0, 0)
	ntGetFinalPathNameByHandleWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameGetFinalPathW[0])), 0, 0, 0, 0)
	ntCreateHardLinkWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameCreateHardLinkW[0])), 0, 0, 0, 0)
	// Symlinks, readlink and the read-only attribute (os_cosmo_nt_link.go).
	ntSetFileAttributesWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameSetFileAttrsW[0])), 0, 0, 0, 0)
	ntCreateSymbolicLinkWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameCreateSymlinkW[0])), 0, 0, 0, 0)
	ntDeviceIoControlFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameDeviceIoControl[0])), 0, 0, 0, 0)
	// flock(2) (ntEmuFlock). Same stance as those above.
	ntLockFileExFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameLockFileEx[0])), 0, 0, 0, 0)
	ntUnlockFileExFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameUnlockFileEx[0])), 0, 0, 0, 0)
	// The host machine, for GOARCH.
	ntIsWow64Process2Fn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameIsWow64Process2[0])), 0, 0, 0, 0)
	// statfs/fstatfs (os_cosmo_nt_statfs.go). Same stance again.
	ntGetVolumePathNameWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameGetVolumePathW[0])), 0, 0, 0, 0)
	ntGetDiskFreeSpaceWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameGetDiskFreeSpaceW[0])), 0, 0, 0, 0)
	ntGetDiskFreeSpaceExWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameGetDiskFreeSpcExW[0])), 0, 0, 0, 0)
	ntGetVolumeInformationWFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameGetVolumeInfoW[0])), 0, 0, 0, 0)

	if bp := ntcall(lla, uintptr(unsafe.Pointer(&ntNameBcryptPrimitives[0])), 0, 0, 0, 0, 0); bp != 0 {
		ntProcessPrngFn = ntcall(gpa, bp, uintptr(unsafe.Pointer(&ntNameProcessPrng[0])), 0, 0, 0, 0)
	}
	if ntProcessPrngFn == 0 {
		if adv := ntcall(lla, uintptr(unsafe.Pointer(&ntNameAdvapi32[0])), 0, 0, 0, 0, 0); adv != 0 {
			ntProcessPrngFn = ntcall(gpa, adv, uintptr(unsafe.Pointer(&ntNameSystemFunction036[0])), 0, 0, 0, 0)
		}
	}

}

// ntResolveWriter resolves WriteFile, GetStdHandle and those std
// handles from the IAT alone. It is what makes a throw before
// ntResolve say something rather than exit 2 in silence.
//
//go:nosplit
func ntResolveWriter() {
	gpa, lla := ntiat[0], ntiat[1]
	if gpa == 0 || lla == 0 {
		return
	}
	k32 := ntcall(lla, uintptr(unsafe.Pointer(&ntNameKernel32[0])), 0, 0, 0, 0, 0)
	if k32 == 0 {
		return
	}
	if ntWriteFileFn == 0 {
		ntWriteFileFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameWriteFile[0])), 0, 0, 0, 0)
	}
	if ntGetStdHandleFn == 0 {
		ntGetStdHandleFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameGetStdHandle[0])), 0, 0, 0, 0)
	}
	if ntGetStdHandleFn == 0 {
		return
	}
	if ntStdout == 0 {
		ntStdout = ntcall(ntGetStdHandleFn, _NT_STD_OUTPUT_HANDLE, 0, 0, 0, 0, 0)
	}
	if ntStderr == 0 {
		ntStderr = ntcall(ntGetStdHandleFn, _NT_STD_ERROR_HANDLE, 0, 0, 0, 0, 0)
	}
}

// Anything else is EBADF. It returns the byte count or a negative errno, as
// write1 does.
//
//go:nosplit
func ntwrite1(fd uintptr, p unsafe.Pointer, n int32) int32 {
	if ntWriteFileFn == 0 || ntGetStdHandleFn == 0 {
		ntResolveWriter()
	}
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

// ntFutexsleep implements futexsleep over WaitOnAddress: compare *addr
// against a stack copy of val and wait with a millisecond timeout (round up;
// any positive ns waits at least 1ms).
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

// ntFutexwakeup implements futexwakeup.
//
//go:nosplit
func ntFutexwakeup(addr *uint32) {
	ntcall(ntWakeByAddressSingleFn, uintptr(unsafe.Pointer(addr)), 0, 0, 0, 0, 0)
}

// ntNewosproc is the NT leg of newosproc: CreateThread with a small (64KiB,
// reservation-only) NT stack; tstart_cosmo_nt pivots onto mp.g0's
// Go-allocated stack, so the Linux bookkeeping (mexit frees the g0 stack) is
// preserved and the NT stack dies with the thread. Same design as real
// cosmo's CloneWindows.
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
		if atomic.Load(&ntExiting) != 0 {
			// CreateThread may fail if called concurrently with ExitProcess.
			lock(&ntDeadlock)
			lock(&ntDeadlock)
		}
		print("runtime: failed to create new OS thread (have ", mcount(), " already)\n")
		throw("newosproc")
	}
	// Close the handle to avoid leaking the thread object if it exits.
	ntcall(ntCloseHandleFn, ret, 0, 0, 0, 0, 0)
}

// ntSystemInfo is the win64 SYSTEM_INFO layout (many bytes).
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

// Memory primitives.

//go:nosplit
func ntVirtualAlloc(v unsafe.Pointer, n uintptr, allocType, prot uintptr) unsafe.Pointer {
	return unsafe.Pointer(ntcall(ntVirtualAllocFn, uintptr(v), n, allocType, prot, 0, 0))
}

//go:nosplit
func ntVirtualFree(v unsafe.Pointer, n uintptr, freeType uintptr) uintptr {
	return ntcall(ntVirtualFreeFn, uintptr(v), n, freeType, 0, 0, 0)
}

// Command line and environment. The NT boot stub fabricates a one-entry argv
// and an empty envp (rt0_cosmo_nt_amd64.s); the real values come from
// GetCommandLineW/GetEnvironmentStringsW in the goargs/goenvs NT branches
// below. Both run inside schedinit AFTER mallocinit (proc.go: mallocinit ...
// goargs; goenvs), so ordinary allocation is fine here.

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

// ntCommandLineToArgv splits a Windows command line into arguments following
// the conventions documented at
// http://daviddeley.com/autohotkey/parameters/parameters.htm#WINARGV.
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
// parsing GetCommandLineW instead of reading the boot block, whose fabricated
// argv is the static "APE". Returns false on non-NT hosts - and on an empty
// command line, keeping the fabricated argv as the fallback.
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

// ntGoenvs is goenvs's NT branch (os_cosmo.go): decode the double-NUL-terminated UTF-16 block from GetEnvironmentStringsW ("A=B\x00C=D\x00\x00") into envs, the same shape goenvs_unix produces; upstream os_windows.go goenvs is the model. The block is deliberately not released: FreeEnvironmentStringsW is not in the wave-1 resolve
// set and the one-shot boot leak is harmless.
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
