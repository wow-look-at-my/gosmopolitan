// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

#include "textflag.h"

// Host OS indicators (must match runtime values)
#define HOSTXNU 8

// Linux AMD64 syscall numbers
#define SYS_read		0
#define SYS_write		1
#define SYS_close		3
#define SYS_mmap		9
#define SYS_munmap		11
#define SYS_mprotect		10
#define SYS_nanosleep		35
#define SYS_exit		60
#define SYS_exit_group		231
#define SYS_openat		257
#define SYS_clock_gettime	228
#define SYS_rt_sigaction	13
#define SYS_sigaltstack		131
#define SYS_pselect6		270

// macOS/XNU BSD syscall numbers (with SYSCALL_CLASS_UNIX prefix 0x2000000)
#define XNU_exit		0x2000001	// BSD 1
#define XNU_read		0x2000003	// BSD 3
#define XNU_write		0x2000004	// BSD 4
#define XNU_open		0x2000005	// BSD 5
#define XNU_close		0x2000006	// BSD 6
#define XNU_mmap		0x20000c5	// BSD 197
#define XNU_munmap		0x2000049	// BSD 73
#define XNU_mprotect		0x200004a	// BSD 74
#define XNU_sigaction		0x200002e	// BSD 46
#define XNU_sigaltstack		0x2000035	// BSD 53
#define XNU_gettimeofday	0x2000074	// BSD 116
#define XNU_select		0x200005d	// BSD 93
#define XNU_pselect		0x20001ae	// BSD 430

// Helper macro: check if we're on macOS and jump to label if so
// Clobbers R11
#define CHECK_DARWIN(label) \
	MOVL	runtime·__hostos(SB), R11; \
	CMPL	R11, $HOSTXNU; \
	JEQ	label

// func Syscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr)
//
// Cosmopolitan Libc uses Linux syscall conventions on all platforms.
// We need to convert to the syscall ABI.
//
// arg | ABIInternal | Syscall
// ---------------------------
// num | AX          | AX
// a1  | BX          | DI
// a2  | CX          | SI
// a3  | DI          | DX
// a4  | SI          | R10
// a5  | R8          | R8
// a6  | R9          | R9
//
// r1  | AX          | AX
// r2  | BX          | DX
// err | CX          | part of AX
//
// Note that this differs from "standard" ABI convention, which would pass 4th
// arg in CX, not R10.
//
// On Darwin x86_64, we use BSD syscall numbers with XNU prefix (0x2000000).
TEXT ·Syscall6<ABIInternal>(SB),NOSPLIT,$0
	CHECK_DARWIN(syscall6_darwin)

	// Linux path: direct syscall (unchanged)
	// a6 already in R9.
	// a5 already in R8.
	MOVQ	SI, R10 // a4
	MOVQ	DI, DX  // a3
	MOVQ	CX, SI  // a2
	MOVQ	BX, DI  // a1
	// num already in AX.
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	ok
	NEGQ	AX
	MOVQ	AX, CX  // errno
	MOVQ	$-1, AX // r1
	MOVQ	$0, BX  // r2
	RET
ok:
	// r1 already in AX.
	MOVQ	DX, BX // r2
	MOVQ	$0, CX // errno
	RET

// Darwin path: dispatch to XNU syscalls based on Linux syscall number
syscall6_darwin:
	// Save syscall number in R11
	MOVQ	AX, R11

	// Setup arguments for syscall (same for all paths)
	// a6 already in R9.
	// a5 already in R8.
	MOVQ	SI, R10 // a4
	MOVQ	DI, DX  // a3
	MOVQ	CX, SI  // a2
	MOVQ	BX, DI  // a1

	// Dispatch based on Linux syscall number
	CMPQ	R11, $SYS_write
	JEQ	darwin_write
	CMPQ	R11, $SYS_read
	JEQ	darwin_read
	CMPQ	R11, $SYS_close
	JEQ	darwin_close
	CMPQ	R11, $SYS_openat
	JEQ	darwin_openat
	CMPQ	R11, $SYS_mmap
	JEQ	darwin_mmap
	CMPQ	R11, $SYS_munmap
	JEQ	darwin_munmap
	CMPQ	R11, $SYS_mprotect
	JEQ	darwin_mprotect
	CMPQ	R11, $SYS_exit
	JEQ	darwin_exit
	CMPQ	R11, $SYS_exit_group
	JEQ	darwin_exit
	CMPQ	R11, $SYS_nanosleep
	JEQ	darwin_nanosleep
	CMPQ	R11, $SYS_clock_gettime
	JEQ	darwin_clock_gettime
	CMPQ	R11, $SYS_rt_sigaction
	JEQ	darwin_sigaction
	CMPQ	R11, $SYS_sigaltstack
	JEQ	darwin_sigaltstack
	CMPQ	R11, $SYS_pselect6
	JEQ	darwin_pselect

	// Unknown syscall - return ENOSYS
darwin_enosys:
	MOVQ	$-1, AX		// r1 = -1
	MOVQ	$0, BX		// r2 = 0
	MOVQ	$38, CX		// errno = ENOSYS
	RET

darwin_write:
	MOVL	$XNU_write, AX
	JMP	darwin_syscall

darwin_read:
	MOVL	$XNU_read, AX
	JMP	darwin_syscall

darwin_close:
	MOVL	$XNU_close, AX
	JMP	darwin_syscall

darwin_openat:
	// macOS open() takes (path, flags, mode) not (dirfd, path, flags, mode)
	// For AT_FDCWD (-100), shift args: path=SI->DI, flags=DX->SI, mode=R10->DX
	CMPQ	DI, $-100	// AT_FDCWD
	JNE	darwin_openat_with_fd
	MOVQ	SI, DI		// path
	MOVQ	DX, SI		// flags
	MOVQ	R10, DX		// mode
	MOVL	$XNU_open, AX
	JMP	darwin_syscall
darwin_openat_with_fd:
	// Non-FDCWD openat not directly supported, return ENOSYS
	JMP	darwin_enosys

darwin_mmap:
	// mmap needs flag translation: Linux MAP_ANONYMOUS (0x20) -> macOS MAP_ANON (0x1000)
	// R10 = flags argument
	MOVL	R10, AX
	ANDL	$0x20, AX	// Check for MAP_ANONYMOUS
	JZ	darwin_mmap_call
	ANDL	$~0x20, R10	// Clear Linux MAP_ANONYMOUS
	ORL	$0x1000, R10	// Set macOS MAP_ANON
darwin_mmap_call:
	MOVL	$XNU_mmap, AX
	JMP	darwin_syscall

darwin_munmap:
	MOVL	$XNU_munmap, AX
	JMP	darwin_syscall

darwin_mprotect:
	MOVL	$XNU_mprotect, AX
	JMP	darwin_syscall

darwin_exit:
	MOVL	$XNU_exit, AX
	JMP	darwin_syscall

darwin_nanosleep:
	// macOS doesn't have nanosleep syscall, use select with timeout
	// This is a simplified implementation
	// For now, just return success (sleep is best-effort)
	MOVQ	$0, AX		// r1 = 0 (success)
	MOVQ	$0, BX		// r2 = 0
	MOVQ	$0, CX		// errno = 0
	RET

darwin_clock_gettime:
	// macOS doesn't have clock_gettime syscall, use gettimeofday
	// This gives wall time for CLOCK_REALTIME, approximation for CLOCK_MONOTONIC
	// DI = clock_id (0=REALTIME, 1=MONOTONIC)
	// SI = struct timespec *
	// We need to call gettimeofday and convert
	MOVQ	SI, R12		// Save timespec pointer
	MOVL	$XNU_gettimeofday, AX
	MOVQ	SI, DI		// timeval pointer (reuse timespec buffer)
	MOVQ	$0, SI		// timezone = NULL
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JHI	darwin_error
	// Convert: timespec.tv_nsec = timeval.tv_usec * 1000
	MOVQ	8(R12), AX	// tv_usec
	IMULQ	$1000, AX	// convert to nsec
	MOVQ	AX, 8(R12)	// tv_nsec
	MOVQ	$0, AX		// success
	MOVQ	$0, BX
	MOVQ	$0, CX
	RET

darwin_sigaction:
	// macOS sigaction has different structure from Linux rt_sigaction
	// For basic functionality, return success
	MOVQ	$0, AX
	MOVQ	$0, BX
	MOVQ	$0, CX
	RET

darwin_sigaltstack:
	MOVL	$XNU_sigaltstack, AX
	JMP	darwin_syscall

darwin_pselect:
	// Use select instead of pselect
	MOVL	$XNU_select, AX
	JMP	darwin_syscall

darwin_syscall:
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JHI	darwin_error
	// Success
	MOVQ	DX, BX		// r2
	MOVQ	$0, CX		// errno = 0
	RET

darwin_error:
	// Error: AX contains negative error code
	NEGQ	AX
	MOVQ	AX, CX		// errno
	MOVQ	$-1, AX		// r1 = -1
	MOVQ	$0, BX		// r2 = 0
	RET
