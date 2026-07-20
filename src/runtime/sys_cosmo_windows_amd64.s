// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Windows syscall implementations for Cosmopolitan APE binaries.
// These functions use Windows API calls instead of Linux syscalls.
// They are called when runtime·iswindows is true.

#include "go_asm.h"
#include "textflag.h"

// Windows constants
#define STD_INPUT_HANDLE	-10
#define STD_OUTPUT_HANDLE	-11
#define STD_ERROR_HANDLE	-12

#define GENERIC_READ		0x80000000
#define GENERIC_WRITE		0x40000000
#define FILE_SHARE_READ		0x00000001
#define FILE_SHARE_WRITE	0x00000002
#define CREATE_ALWAYS		2
#define OPEN_EXISTING		3
#define FILE_ATTRIBUTE_NORMAL	0x80

#define MEM_COMMIT		0x1000
#define MEM_RESERVE		0x2000
#define MEM_RELEASE		0x8000
#define PAGE_READWRITE		0x04
#define PAGE_EXECUTE_READWRITE	0x40

#define INFINITE		0xFFFFFFFF

// Import Address Table entries (set by PE loader)
// These are at fixed addresses in our PE image
// Base address: 0x140000000
// IAT at RVA 0x2080
#define IAT_BASE		0x140002080

// Kernel32 function indices in IAT
#define IAT_GetStdHandle	(IAT_BASE + 0)
#define IAT_WriteFile		(IAT_BASE + 8)
#define IAT_ExitProcess		(IAT_BASE + 16)
#define IAT_ReadFile		(IAT_BASE + 24)
#define IAT_CreateFileW		(IAT_BASE + 32)
#define IAT_CloseHandle		(IAT_BASE + 40)
#define IAT_VirtualAlloc	(IAT_BASE + 48)
#define IAT_VirtualFree		(IAT_BASE + 56)
#define IAT_CreateThread	(IAT_BASE + 64)
#define IAT_GetCurrentThreadId	(IAT_BASE + 72)
#define IAT_GetCurrentProcessId	(IAT_BASE + 80)
#define IAT_Sleep		(IAT_BASE + 88)
#define IAT_QueryPerformanceCounter	(IAT_BASE + 96)
#define IAT_QueryPerformanceFrequency	(IAT_BASE + 104)
#define IAT_GetSystemTimePreciseAsFileTime	(IAT_BASE + 112)
#define IAT_GetLastError	(IAT_BASE + 120)
#define IAT_SetLastError	(IAT_BASE + 128)
#define IAT_TlsAlloc		(IAT_BASE + 136)
#define IAT_TlsFree		(IAT_BASE + 144)
#define IAT_TlsGetValue		(IAT_BASE + 152)
#define IAT_TlsSetValue		(IAT_BASE + 160)
#define IAT_WaitForSingleObject	(IAT_BASE + 168)
#define IAT_CreateEventW	(IAT_BASE + 176)
#define IAT_SetEvent		(IAT_BASE + 184)
#define IAT_ResetEvent		(IAT_BASE + 192)
#define IAT_GetModuleFileNameW	(IAT_BASE + 200)
#define IAT_GetEnvironmentVariableW	(IAT_BASE + 208)

// Windows runtime state (set by PE stub)
DATA runtime·iswindows(SB)/4, $0
GLOBL runtime·iswindows(SB), NOPTR, $4

// Windows TLS slot for G pointer
DATA runtime·windowstlsslot(SB)/4, $0
GLOBL runtime·windowstlsslot(SB), NOPTR, $4

// Windows API wrappers using Microsoft x64 calling convention:
// Arguments: RCX, RDX, R8, R9, then stack
// Caller saves: RAX, RCX, RDX, R8, R9, R10, R11
// Callee saves: RBX, RBP, RDI, RSI, R12-R15
// Return: RAX
// Must maintain 16-byte stack alignment
// Shadow space: 32 bytes reserved by caller

// func winExitProcess(code uint32)
TEXT runtime·winExitProcess(SB),NOSPLIT,$32-4
	MOVL	code+0(FP), CX
	MOVQ	runtime·_ExitProcess(SB), AX
	CALL	AX
	INT	$3		// should not return

// func winWriteFile(handle uintptr, buf *byte, n int32) int32
TEXT runtime·winWriteFile(SB),NOSPLIT,$48-28
	MOVQ	handle+0(FP), CX
	MOVQ	buf+8(FP), DX
	MOVL	n+16(FP), R8
	LEAQ	ret+24(FP), R9		// lpNumberOfBytesWritten
	MOVQ	$0, 32(SP)		// lpOverlapped = NULL
	MOVQ	runtime·_WriteFile(SB), AX
	CALL	AX
	// WriteFile returns BOOL, actual bytes written in *R9
	MOVL	ret+24(FP), AX
	RET

// func winReadFile(handle uintptr, buf *byte, n int32) int32
TEXT runtime·winReadFile(SB),NOSPLIT,$48-28
	MOVQ	handle+0(FP), CX
	MOVQ	buf+8(FP), DX
	MOVL	n+16(FP), R8
	LEAQ	ret+24(FP), R9		// lpNumberOfBytesRead
	MOVQ	$0, 32(SP)		// lpOverlapped = NULL
	MOVQ	runtime·_ReadFile(SB), AX
	CALL	AX
	MOVL	ret+24(FP), AX
	RET

// func winGetStdHandle(nStdHandle int32) uintptr
TEXT runtime·winGetStdHandle(SB),NOSPLIT,$32-16
	MOVL	nStdHandle+0(FP), CX
	MOVQ	runtime·_GetStdHandle(SB), AX
	CALL	AX
	MOVQ	AX, ret+8(FP)
	RET

// func winCloseHandle(handle uintptr) bool
TEXT runtime·winCloseHandle(SB),NOSPLIT,$32-16
	MOVQ	handle+0(FP), CX
	MOVQ	runtime·_CloseHandle(SB), AX
	CALL	AX
	MOVB	AL, ret+8(FP)
	RET

// func winVirtualAlloc(addr uintptr, size uintptr, alloctype uint32, protect uint32) uintptr
TEXT runtime·winVirtualAlloc(SB),NOSPLIT,$32-40
	MOVQ	addr+0(FP), CX
	MOVQ	size+8(FP), DX
	MOVL	alloctype+16(FP), R8
	MOVL	protect+20(FP), R9
	MOVQ	runtime·_VirtualAlloc(SB), AX
	CALL	AX
	MOVQ	AX, ret+32(FP)
	RET

// func winVirtualFree(addr uintptr, size uintptr, freetype uint32) bool
TEXT runtime·winVirtualFree(SB),NOSPLIT,$32-25
	MOVQ	addr+0(FP), CX
	MOVQ	size+8(FP), DX
	MOVL	freetype+16(FP), R8
	MOVQ	runtime·_VirtualFree(SB), AX
	CALL	AX
	MOVB	AL, ret+24(FP)
	RET

// func winGetCurrentThreadId() uint32
TEXT runtime·winGetCurrentThreadId(SB),NOSPLIT,$32-4
	MOVQ	runtime·_GetCurrentThreadId(SB), AX
	CALL	AX
	MOVL	AX, ret+0(FP)
	RET

// func winGetCurrentProcessId() uint32
TEXT runtime·winGetCurrentProcessId(SB),NOSPLIT,$32-4
	MOVQ	runtime·_GetCurrentProcessId(SB), AX
	CALL	AX
	MOVL	AX, ret+0(FP)
	RET

// func winSleep(ms uint32)
TEXT runtime·winSleep(SB),NOSPLIT,$32-4
	MOVL	ms+0(FP), CX
	MOVQ	runtime·_Sleep(SB), AX
	CALL	AX
	RET

// func winQueryPerformanceCounter() int64
TEXT runtime·winQueryPerformanceCounter(SB),NOSPLIT,$40-8
	LEAQ	ret+0(FP), CX
	MOVQ	runtime·_QueryPerformanceCounter(SB), AX
	CALL	AX
	RET

// func winQueryPerformanceFrequency() int64
TEXT runtime·winQueryPerformanceFrequency(SB),NOSPLIT,$40-8
	LEAQ	ret+0(FP), CX
	MOVQ	runtime·_QueryPerformanceFrequency(SB), AX
	CALL	AX
	RET

// func winTlsAlloc() uint32
TEXT runtime·winTlsAlloc(SB),NOSPLIT,$32-4
	MOVQ	runtime·_TlsAlloc(SB), AX
	CALL	AX
	MOVL	AX, ret+0(FP)
	RET

// func winTlsSetValue(slot uint32, value uintptr) bool
TEXT runtime·winTlsSetValue(SB),NOSPLIT,$32-17
	MOVL	slot+0(FP), CX
	MOVQ	value+8(FP), DX
	MOVQ	runtime·_TlsSetValue(SB), AX
	CALL	AX
	MOVB	AL, ret+16(FP)
	RET

// func winTlsGetValue(slot uint32) uintptr
TEXT runtime·winTlsGetValue(SB),NOSPLIT,$32-16
	MOVL	slot+0(FP), CX
	MOVQ	runtime·_TlsGetValue(SB), AX
	CALL	AX
	MOVQ	AX, ret+8(FP)
	RET

// func winCreateThread(fn uintptr, arg uintptr) uintptr
TEXT runtime·winCreateThread(SB),NOSPLIT,$48-24
	MOVQ	$0, CX			// lpThreadAttributes
	MOVQ	$0, DX			// dwStackSize (default)
	MOVQ	fn+0(FP), R8		// lpStartAddress
	MOVQ	arg+8(FP), R9		// lpParameter
	MOVQ	$0, 32(SP)		// dwCreationFlags
	MOVQ	$0, 40(SP)		// lpThreadId
	MOVQ	runtime·_CreateThread(SB), AX
	CALL	AX
	MOVQ	AX, ret+16(FP)
	RET

// func winWaitForSingleObject(handle uintptr, ms uint32) uint32
TEXT runtime·winWaitForSingleObject(SB),NOSPLIT,$32-20
	MOVQ	handle+0(FP), CX
	MOVL	ms+8(FP), DX
	MOVQ	runtime·_WaitForSingleObject(SB), AX
	CALL	AX
	MOVL	AX, ret+16(FP)
	RET

// func winCreateEventW(manual bool, initial bool) uintptr
TEXT runtime·winCreateEventW(SB),NOSPLIT,$40-24
	MOVQ	$0, CX			// lpEventAttributes
	MOVBLZX	manual+0(FP), DX	// bManualReset
	MOVBLZX	initial+1(FP), R8	// bInitialState
	MOVQ	$0, R9			// lpName
	MOVQ	runtime·_CreateEventW(SB), AX
	CALL	AX
	MOVQ	AX, ret+8(FP)
	RET

// func winSetEvent(handle uintptr) bool
TEXT runtime·winSetEvent(SB),NOSPLIT,$32-16
	MOVQ	handle+0(FP), CX
	MOVQ	runtime·_SetEvent(SB), AX
	CALL	AX
	MOVB	AL, ret+8(FP)
	RET

// func winResetEvent(handle uintptr) bool
TEXT runtime·winResetEvent(SB),NOSPLIT,$32-16
	MOVQ	handle+0(FP), CX
	MOVQ	runtime·_ResetEvent(SB), AX
	CALL	AX
	MOVB	AL, ret+8(FP)
	RET

// Windows function pointers (filled in by initWindows)
DATA runtime·_GetStdHandle(SB)/8, $0
DATA runtime·_WriteFile(SB)/8, $0
DATA runtime·_ReadFile(SB)/8, $0
DATA runtime·_ExitProcess(SB)/8, $0
DATA runtime·_CloseHandle(SB)/8, $0
DATA runtime·_VirtualAlloc(SB)/8, $0
DATA runtime·_VirtualFree(SB)/8, $0
DATA runtime·_CreateThread(SB)/8, $0
DATA runtime·_GetCurrentThreadId(SB)/8, $0
DATA runtime·_GetCurrentProcessId(SB)/8, $0
DATA runtime·_Sleep(SB)/8, $0
DATA runtime·_QueryPerformanceCounter(SB)/8, $0
DATA runtime·_QueryPerformanceFrequency(SB)/8, $0
DATA runtime·_TlsAlloc(SB)/8, $0
DATA runtime·_TlsSetValue(SB)/8, $0
DATA runtime·_TlsGetValue(SB)/8, $0
DATA runtime·_WaitForSingleObject(SB)/8, $0
DATA runtime·_CreateEventW(SB)/8, $0
DATA runtime·_SetEvent(SB)/8, $0
DATA runtime·_ResetEvent(SB)/8, $0

GLOBL runtime·_GetStdHandle(SB), NOPTR, $8
GLOBL runtime·_WriteFile(SB), NOPTR, $8
GLOBL runtime·_ReadFile(SB), NOPTR, $8
GLOBL runtime·_ExitProcess(SB), NOPTR, $8
GLOBL runtime·_CloseHandle(SB), NOPTR, $8
GLOBL runtime·_VirtualAlloc(SB), NOPTR, $8
GLOBL runtime·_VirtualFree(SB), NOPTR, $8
GLOBL runtime·_CreateThread(SB), NOPTR, $8
GLOBL runtime·_GetCurrentThreadId(SB), NOPTR, $8
GLOBL runtime·_GetCurrentProcessId(SB), NOPTR, $8
GLOBL runtime·_Sleep(SB), NOPTR, $8
GLOBL runtime·_QueryPerformanceCounter(SB), NOPTR, $8
GLOBL runtime·_QueryPerformanceFrequency(SB), NOPTR, $8
GLOBL runtime·_TlsAlloc(SB), NOPTR, $8
GLOBL runtime·_TlsSetValue(SB), NOPTR, $8
GLOBL runtime·_TlsGetValue(SB), NOPTR, $8
GLOBL runtime·_WaitForSingleObject(SB), NOPTR, $8
GLOBL runtime·_CreateEventW(SB), NOPTR, $8
GLOBL runtime·_SetEvent(SB), NOPTR, $8
GLOBL runtime·_ResetEvent(SB), NOPTR, $8
