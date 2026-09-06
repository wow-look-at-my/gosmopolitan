// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

#include "textflag.h"

// Host OS indicators (must match runtime values)
#define HOSTWINDOWS 2
#define HOSTXNU 8

// Linux AMD64 syscall numbers
#define SYS_read		0
#define SYS_write		1
#define SYS_close		3
#define SYS_lseek		8
#define SYS_mmap		9
#define SYS_munmap		11
#define SYS_mprotect		10
#define SYS_pread64		17
#define SYS_pwrite64		18
#define SYS_nanosleep		35
#define SYS_exit		60
#define SYS_exit_group		231
#define SYS_openat		257
#define SYS_clock_gettime	228
#define SYS_rt_sigaction	13
#define SYS_sigaltstack		131
#define SYS_pselect6		270

// Linux amd64 numbers for the metadata wave, as
// syscall/zsysnum_cosmo_amd64.go records them.
#define SYS_flock		73
#define SYS_fsync		74
#define SYS_fdatasync		75
#define SYS_sync		162
#define SYS_truncate		76
#define SYS_ftruncate		77
#define SYS_fchdir		81
#define SYS_fchmod		91
#define SYS_fchown		93
#define SYS_setuid		105
#define SYS_setgid		106
#define SYS_setreuid		113
#define SYS_setregid		114
#define SYS_getgroups		115
#define SYS_setgroups		116
#define SYS_getpgid		121
#define SYS_statfs		137
#define SYS_fstatfs		138
#define SYS_getpriority		140
#define SYS_setpriority		141
#define SYS_chroot		161
#define SYS_fchownat		260
#define SYS_linkat		265
#define SYS_symlinkat		266
#define SYS_fchmodat		268

#define LINUX_AT_FDCWD			-100
#define LINUX_AT_SYMLINK_NOFOLLOW	0x100

// macOS/XNU BSD syscall numbers (with SYSCALL_CLASS_UNIX prefix 0x2000000)
#define XNU_exit		0x2000001	// BSD 1
#define XNU_read		0x2000003	// BSD 3
#define XNU_write		0x2000004	// BSD 4
#define XNU_open		0x2000005	// BSD 5
#define XNU_openat		0x20001cf	// BSD 463
#define XNU_close		0x2000006	// BSD 6
#define XNU_mmap		0x20000c5	// BSD 197
#define XNU_munmap		0x2000049	// BSD 73
#define XNU_mprotect		0x200004a	// BSD 74
#define XNU_sigaction		0x200002e	// BSD 46
#define XNU_sigreturn		0x20000b8	// BSD 184
#define XNU_sigaltstack		0x2000035	// BSD 53
#define XNU_gettimeofday	0x2000074	// BSD 116
#define XNU_select		0x200005d	// BSD 93
#define XNU_pselect		0x20001ae	// BSD 430
#define XNU_pread		0x2000099	// BSD 153
#define XNU_pwrite		0x200009a	// BSD 154
#define XNU_lseek		0x20000c7	// BSD 199

// Metadata and credential syscalls (2026-09-02). Every BSD number here
// is the one syscall/zsysnum_darwin_amd64.go records - the tree's own
// authority for XNU numbering - not a remembered value. Anything whose
// number that file does not carry is deliberately absent below rather
// than guessed: a wrong number does not fail, it calls a DIFFERENT
// syscall. That is why the *at family and utimensat stay ENOSYS here
// while arm64 serves them (arm64 resolves by NAME through dlsym, so it
// never needs a number at all).
#define XNU_fchdir		0x200000d	// BSD 13
#define XNU_mknod		0x200000e	// BSD 14
#define XNU_setuid		0x2000017	// BSD 23
#define XNU_chroot		0x200003d	// BSD 61
#define XNU_getgroups		0x200004f	// BSD 79
#define XNU_setgroups		0x2000050	// BSD 80
#define XNU_sync		0x2000024	// BSD 36
#define XNU_fsync		0x200005f	// BSD 95
#define XNU_fdatasync		0x20000bb	// BSD 187
#define XNU_setpriority		0x2000060	// BSD 96
#define XNU_getpriority		0x2000064	// BSD 100
#define XNU_fchown		0x200007b	// BSD 123
#define XNU_fchmod		0x200007c	// BSD 124
#define XNU_setreuid		0x200007e	// BSD 126
#define XNU_setregid		0x200007f	// BSD 127
#define XNU_flock		0x2000083	// BSD 131
#define XNU_getpgid		0x2000097	// BSD 151
#define XNU_setgid		0x20000b5	// BSD 181
#define XNU_getrlimit		0x20000c2	// BSD 194
#define XNU_setrlimit		0x20000c3	// BSD 195
#define XNU_truncate		0x20000c8	// BSD 200
#define XNU_ftruncate		0x20000c9	// BSD 201
#define XNU_sendfile		0x2000151	// BSD 337
#define XNU_statfs64		0x2000159	// BSD 345
#define XNU_fstatfs64		0x200015a	// BSD 346

// The classic path-based calls the *at family is served with when its
// directory argument is AT_FDCWD. XNU's own *at syscalls sit in the
// 464+ range that zsysnum_darwin_amd64.go does not record, and these
// do exactly the same work for the only case the standard library
// reaches (os.Chmod, os.Lchown, os.Link and os.Symlink all pass
// AT_FDCWD).
#define XNU_link		0x2000009	// BSD 9
#define XNU_chmod		0x200000f	// BSD 15
#define XNU_chown		0x2000010	// BSD 16
#define XNU_symlink		0x2000039	// BSD 57
#define XNU_lchown		0x200016c	// BSD 364

// The Apple struct statfs the syscall package allocates on the
// emulation's behalf (bigbuf_cosmo.go). Its size travels in a3 so a
// caller that arrived with a Linux-layout buffer is refused instead of
// overrun - the same fail-closed guard the arm64 path applies in Go.
#define APPLE_STATFS_SIZE	2168

// Helper macro: check if we're on macOS and jump to label if so
// Clobbers R11
#define CHECK_DARWIN(label) \
	MOVL	runtime·__hostos(SB), R11; \
	CMPL	R11, $HOSTXNU; \
	JEQ	label

// Helper macro: check if we're on Windows NT and jump to label if so.
// No raw SYSCALL may execute when __hostos is Windows.
// Clobbers R11
#define CHECK_WINDOWS(label) \
	MOVL	runtime·__hostos(SB), R11; \
	CMPL	R11, $HOSTWINDOWS; \
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
// The 48-byte frame belongs to darwin_nanosleep, the one case here that
// has to build a struct the caller did not pass: a timeval for select,
// and the two timevals that measure how much of the request is left when
// a signal cuts the sleep short. Every other path ignores it.
TEXT ·Syscall6<ABIInternal>(SB),NOSPLIT,$48
	// Safety net: on NT hosts everything not routed through the
	// WindowsFns table (syscall_cosmo_nt.go) is ENOSYS - never a raw
	// SYSCALL.
	CHECK_WINDOWS(syscall6_windows)
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
	CMPQ	R11, $SYS_pread64
	JEQ	darwin_pread
	CMPQ	R11, $SYS_pwrite64
	JEQ	darwin_pwrite
	CMPQ	R11, $SYS_lseek
	JEQ	darwin_lseek
	CMPQ	R11, $SYS_fsync
	JEQ	darwin_fsync
	CMPQ	R11, $SYS_ftruncate
	JEQ	darwin_ftruncate
	CMPQ	R11, $SYS_truncate
	JEQ	darwin_truncate
	CMPQ	R11, $SYS_fchmod
	JEQ	darwin_fchmod
	CMPQ	R11, $SYS_fchown
	JEQ	darwin_fchown
	CMPQ	R11, $SYS_fchdir
	JEQ	darwin_fchdir
	CMPQ	R11, $SYS_flock
	JEQ	darwin_flock
	CMPQ	R11, $SYS_fdatasync
	JEQ	darwin_fdatasync
	CMPQ	R11, $SYS_sync
	JEQ	darwin_sync
	CMPQ	R11, $SYS_chroot
	JEQ	darwin_chroot
	CMPQ	R11, $SYS_getgroups
	JEQ	darwin_getgroups
	CMPQ	R11, $SYS_setgroups
	JEQ	darwin_setgroups
	CMPQ	R11, $SYS_setpriority
	JEQ	darwin_setpriority
	CMPQ	R11, $SYS_getpriority
	JEQ	darwin_getpriority
	CMPQ	R11, $SYS_setuid
	JEQ	darwin_setuid
	CMPQ	R11, $SYS_setgid
	JEQ	darwin_setgid
	CMPQ	R11, $SYS_setreuid
	JEQ	darwin_setreuid
	CMPQ	R11, $SYS_setregid
	JEQ	darwin_setregid
	CMPQ	R11, $SYS_getpgid
	JEQ	darwin_getpgid
	CMPQ	R11, $SYS_statfs
	JEQ	darwin_statfs
	CMPQ	R11, $SYS_fstatfs
	JEQ	darwin_fstatfs
	CMPQ	R11, $SYS_fchmodat
	JEQ	darwin_fchmodat
	CMPQ	R11, $SYS_fchownat
	JEQ	darwin_fchownat
	CMPQ	R11, $SYS_linkat
	JEQ	darwin_linkat
	CMPQ	R11, $SYS_symlinkat
	JEQ	darwin_symlinkat

	// Unknown syscall - return ENOSYS
darwin_enosys:
syscall6_windows:
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
	// The Linux O_* bits are not Apple's. Untranslated, O_CREAT (0x40)
	// arrives as Apple O_SHLOCK and os.Create makes no file.
	CMPQ	DI, $-100	// AT_FDCWD
	JNE	darwin_openat_with_fd
	// Apple open(path, flags, mode): shift the dirfd off the front.
	MOVQ	SI, DI		// path
	MOVQ	DX, SI		// flags
	MOVQ	R10, DX		// mode
	XCHGQ	SI, DX		// the helper reads and writes DX
	CALL	runtime·cosmo_xlat_oflags_dx(SB)
	XCHGQ	SI, DX
	MOVL	$XNU_open, AX
	JMP	darwin_syscall
darwin_openat_with_fd:
	// Apple's openat takes the same argument order, so only the flags
	// move. A real descriptor is never the AT_FDCWD sentinel, so the
	// -100/-2 difference cannot reach here.
	CALL	runtime·cosmo_xlat_oflags_dx(SB)
	MOVL	$XNU_openat, AX
	JMP	darwin_syscall

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

// nanosleep(req *timespec, rem *timespec).
//
// XNU has no nanosleep syscall. select with a timeout and no descriptors
// is a sleep, which is what the runtime's own usleep already does on
// this host. This used to return success WITHOUT SLEEPING, so every
// caller ran straight through its delay and could not tell.
//
// Linux fills rem with the unslept remainder when a signal cuts the
// sleep short, and BSD select does not update its timeout, so the
// remainder is measured here: gettimeofday before and after, subtract
// the elapsed time from the request, clamp at zero. A caller that loops
// on EINTR with rem needs that to be real, or it sleeps twice.
//
//	0(SP)  timeval handed to select
//	16(SP) timeval before the sleep
//	32(SP) timeval after it
darwin_nanosleep:
	MOVQ	DI, R12		// req
	MOVQ	SI, R13		// rem, may be NULL
	CMPQ	R12, $0
	JEQ	darwin_efault

	LEAQ	16(SP), DI	// before
	MOVQ	$0, SI
	XORL	DX, DX		// post-Sierra third argument; see clock_gettime
	MOVL	$XNU_gettimeofday, AX
	SYSCALL

	MOVQ	0(R12), AX	// tv_sec
	MOVQ	AX, 0(SP)
	MOVQ	8(R12), AX	// tv_nsec
	XORQ	DX, DX
	MOVQ	$1000, CX
	DIVQ	CX
	MOVQ	AX, 8(SP)	// tv_usec

	MOVQ	$0, DI		// nfds
	MOVQ	$0, SI		// readfds
	MOVQ	$0, DX		// writefds
	MOVQ	$0, R10		// exceptfds
	LEAQ	0(SP), R8	// timeout
	MOVL	$XNU_select, AX
	SYSCALL
	JCS	darwin_nanosleep_intr

	// The timeout expired, so the whole request was slept.
	CMPQ	R13, $0
	JEQ	darwin_nanosleep_ok
	MOVQ	$0, 0(R13)
	MOVQ	$0, 8(R13)
darwin_nanosleep_ok:
	MOVQ	$0, AX		// r1
	MOVQ	$0, BX		// r2
	MOVQ	$0, CX		// errno
	RET

darwin_nanosleep_intr:
	MOVQ	AX, R11		// Apple errno, translated at the end
	CMPQ	R13, $0
	JEQ	darwin_nanosleep_err

	LEAQ	32(SP), DI	// after
	MOVQ	$0, SI
	XORL	DX, DX
	MOVL	$XNU_gettimeofday, AX
	SYSCALL

	// elapsed = (after.sec - before.sec)*1e9 + (after.usec - before.usec)*1e3
	MOVQ	32(SP), AX
	SUBQ	16(SP), AX
	IMULQ	$1000000000, AX
	MOVQ	40(SP), DX
	SUBQ	24(SP), DX
	IMULQ	$1000, DX
	ADDQ	DX, AX

	// remaining = requested - elapsed, floored at zero
	MOVQ	0(R12), CX
	IMULQ	$1000000000, CX
	ADDQ	8(R12), CX
	SUBQ	AX, CX
	JGE	darwin_nanosleep_rem
	XORQ	CX, CX
darwin_nanosleep_rem:
	MOVQ	CX, AX
	XORQ	DX, DX
	MOVQ	$1000000000, R9
	DIVQ	R9
	MOVQ	AX, 0(R13)	// rem.tv_sec
	MOVQ	DX, 8(R13)	// rem.tv_nsec

darwin_nanosleep_err:
	MOVQ	R11, AX
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	MOVQ	AX, CX		// errno (Linux numbering)
	MOVQ	$-1, AX		// r1
	MOVQ	$0, BX		// r2
	RET

darwin_efault:
	MOVQ	$-1, AX
	MOVQ	$0, BX
	MOVQ	$14, CX		// EFAULT
	RET

darwin_einval:
	MOVQ	$-1, AX
	MOVQ	$0, BX
	MOVQ	$22, CX		// EINVAL
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
	JCS	darwin_error	// XNU signals failure with the carry flag
	// Convert: timespec.tv_nsec = timeval.tv_usec * 1000
	MOVQ	8(R12), AX	// tv_usec
	IMULQ	$1000, AX	// convert to nsec
	MOVQ	AX, 8(R12)	// tv_nsec
	MOVQ	$0, AX		// success
	MOVQ	$0, BX
	MOVQ	$0, CX
	RET

// rt_sigaction(sig, new, old, sigsetsize). The struct, the signal
// number, the mask width and the flag bits all differ, so the work is
// done in Go (sigaction_cosmo_amd64.go). This must be a real CALL with
// outgoing arguments in this frame - a tail JMP would leave the $48
// frame in place and shift every FP offset the callee sees.
darwin_sigaction:
	MOVQ	DI, 0(SP)
	MOVQ	SI, 8(SP)
	MOVQ	DX, 16(SP)
	MOVQ	R10, 24(SP)
	CALL	·darwinSigaction(SB)
	MOVQ	32(SP), AX	// r1
	MOVQ	40(SP), CX	// errno
	MOVQ	$0, BX		// r2
	RET

darwin_sigaltstack:
	MOVL	$XNU_sigaltstack, AX
	JMP	darwin_syscall

darwin_pselect:
	// Use select instead of pselect
	MOVL	$XNU_select, AX
	JMP	darwin_syscall

darwin_pread:
	MOVL	$XNU_pread, AX
	JMP	darwin_syscall

darwin_pwrite:
	MOVL	$XNU_pwrite, AX
	JMP	darwin_syscall

darwin_lseek:
	MOVL	$XNU_lseek, AX
	JMP	darwin_syscall

darwin_fsync:
	MOVL	$XNU_fsync, AX
	JMP	darwin_syscall

darwin_ftruncate:
	MOVL	$XNU_ftruncate, AX
	JMP	darwin_syscall

darwin_truncate:
	MOVL	$XNU_truncate, AX
	JMP	darwin_syscall

darwin_fchmod:
	MOVL	$XNU_fchmod, AX
	JMP	darwin_syscall

darwin_fchown:
	MOVL	$XNU_fchown, AX
	JMP	darwin_syscall

darwin_fchdir:
	MOVL	$XNU_fchdir, AX
	JMP	darwin_syscall

darwin_fdatasync:
	MOVL	$XNU_fdatasync, AX
	JMP	darwin_syscall

// sync returns void on XNU, so AX holds nothing a caller may read. The
// Linux syscall returns 0, and that is what goes back.
darwin_sync:
	MOVL	$XNU_sync, AX
	SYSCALL
	MOVQ	$0, AX		// r1
	MOVQ	$0, BX		// r2
	MOVQ	$0, CX		// errno
	RET

darwin_flock:
	// LOCK_SH/LOCK_EX/LOCK_NB/LOCK_UN are 1/2/4/8 on both systems:
	// Linux took them from BSD, which is what XNU still serves.
	MOVL	$XNU_flock, AX
	JMP	darwin_syscall

darwin_chroot:
	MOVL	$XNU_chroot, AX
	JMP	darwin_syscall

darwin_getgroups:
	// gid_t is a 32-bit unsigned integer on both systems.
	MOVL	$XNU_getgroups, AX
	JMP	darwin_syscall

darwin_setgroups:
	MOVL	$XNU_setgroups, AX
	JMP	darwin_syscall

darwin_setpriority:
	// PRIO_PROCESS/PRIO_PGRP/PRIO_USER are 0/1/2 on both systems.
	MOVL	$XNU_setpriority, AX
	JMP	darwin_syscall

darwin_setuid:
	MOVL	$XNU_setuid, AX
	JMP	darwin_syscall

darwin_setgid:
	MOVL	$XNU_setgid, AX
	JMP	darwin_syscall

darwin_setreuid:
	MOVL	$XNU_setreuid, AX
	JMP	darwin_syscall

darwin_setregid:
	MOVL	$XNU_setregid, AX
	JMP	darwin_syscall

darwin_getpgid:
	MOVL	$XNU_getpgid, AX
	JMP	darwin_syscall

// statfs/fstatfs fill an Apple struct statfs, which the syscall package
// allocates and converts (bigbuf_cosmo.go). a3 carries that buffer's
// size; anything smaller is refused rather than overrun.
darwin_statfs:
	CMPQ	DX, $APPLE_STATFS_SIZE
	JB	darwin_einval
	MOVL	$XNU_statfs64, AX
	JMP	darwin_syscall

darwin_fstatfs:
	CMPQ	DX, $APPLE_STATFS_SIZE
	JB	darwin_einval
	MOVL	$XNU_fstatfs64, AX
	JMP	darwin_syscall

// The *at family, served with the classic path calls for AT_FDCWD.
// A real directory descriptor is refused rather than resolved against
// the process working directory, which would silently act on the wrong
// file. os.Chmod, os.Lchown, os.Link and os.Symlink all pass AT_FDCWD.

darwin_fchmodat:
	// fchmodat(dirfd, path, mode) -> chmod(path, mode).
	CMPQ	DI, $LINUX_AT_FDCWD
	JNE	darwin_enosys
	MOVQ	SI, DI		// path
	MOVQ	DX, SI		// mode
	MOVL	$XNU_chmod, AX
	JMP	darwin_syscall

darwin_fchownat:
	// fchownat(dirfd, path, uid, gid, flags) -> chown/lchown(path, uid, gid).
	CMPQ	DI, $LINUX_AT_FDCWD
	JNE	darwin_enosys
	MOVQ	SI, DI		// path
	MOVQ	DX, SI		// uid
	MOVQ	R10, DX		// gid
	CMPQ	R8, $0
	JEQ	darwin_fchownat_follow
	CMPQ	R8, $LINUX_AT_SYMLINK_NOFOLLOW
	JNE	darwin_enosys
	MOVL	$XNU_lchown, AX
	JMP	darwin_syscall
darwin_fchownat_follow:
	MOVL	$XNU_chown, AX
	JMP	darwin_syscall

darwin_linkat:
	// linkat(olddirfd, oldpath, newdirfd, newpath, flags) -> link(old, new).
	// AT_SYMLINK_FOLLOW has no expressible alternative here: link(2)
	// follows, so a request NOT to follow is refused rather than
	// silently followed.
	CMPQ	DI, $LINUX_AT_FDCWD
	JNE	darwin_enosys
	CMPQ	DX, $LINUX_AT_FDCWD
	JNE	darwin_enosys
	MOVQ	SI, DI		// oldpath
	MOVQ	R10, SI		// newpath
	MOVL	$XNU_link, AX
	JMP	darwin_syscall

darwin_symlinkat:
	// symlinkat(target, newdirfd, linkpath) -> symlink(target, linkpath).
	// Only the SECOND argument is a descriptor; the target is a plain
	// string the kernel never resolves.
	CMPQ	SI, $LINUX_AT_FDCWD
	JNE	darwin_enosys
	MOVQ	DX, SI		// linkpath
	MOVL	$XNU_symlink, AX
	JMP	darwin_syscall

// getpriority is the one caller here that cannot read its own result.
// XNU returns the nice value, where -1 is legal, and the LINUX SYSCALL
// returns 20-nice so its result is never negative. The carry flag - not
// the value - says whether the call failed, so the bias can be applied
// unconditionally on the success path.
darwin_getpriority:
	MOVL	$XNU_getpriority, AX
	SYSCALL
	JCS	darwin_error
	MOVQ	$20, R11
	SUBQ	AX, R11
	MOVQ	R11, AX		// r1 = 20 - nice
	MOVQ	$0, BX		// r2
	MOVQ	$0, CX		// errno = 0
	RET

darwin_syscall:
	SYSCALL
	JCS	darwin_error
	// Success
	MOVQ	DX, BX		// r2
	MOVQ	$0, CX		// errno = 0
	RET

// XNU reports failure by SETTING THE CARRY FLAG and returning a
// POSITIVE Apple errno - not the negative-return convention Linux uses.
// The carry flag decides, and runtime·cosmo_xlat_errno_ax turns Apple's
// number into the Linux one Go compares against. arm64 does the same
// through cosmo_xlat_errno_r0, over the same table.
darwin_error:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	MOVQ	AX, CX		// errno (Linux numbering)
	MOVQ	$-1, AX		// r1 = -1
	MOVQ	$0, BX		// r2 = 0
	RET

// func xnuSigaction(sig, new, old uintptr) (r1, errno uintptr)
//
// The raw __sigaction syscall over Apple's KERNEL struct sigaction.
// darwinSigaction builds both structs; this only carries them across.
TEXT ·xnuSigaction(SB),NOSPLIT,$0-40
	MOVQ	sig+0(FP), DI
	MOVQ	new+8(FP), SI
	MOVQ	old+16(FP), DX
	MOVL	$XNU_sigaction, AX
	SYSCALL
	JCS	xnusigaction_err
	MOVQ	AX, r1+24(FP)
	MOVQ	$0, errno+32(FP)
	RET
xnusigaction_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	MOVQ	AX, errno+32(FP)
	MOVQ	$-1, AX
	MOVQ	AX, r1+24(FP)
	RET

// func sigactionTramp()
//
// The sa_tramp of every handler installed through this package. The
// KERNEL enters it, not Go, with:
//
//	DI  handler
//	SI  infostyle, which sigreturn needs back
//	DX  sig (APPLE numbering)
//	CX  info
//	R8  ctx
//	R9  token, which sigreturn needs back
//
// It calls the handler with the (sig, info, ctx) arguments a Linux
// handler is entered with, then issues sigreturn(uctx, infostyle,
// token), which the kernel leaves to the trampoline.
//
// runtime·cosmoXnuSigtramp does not fit here: it drops the handler and
// dispatches through runtime·sigtramp, which is right for the handlers
// the runtime installs and wrong for a caller's own.
//
// info and ctx keep their Apple layouts. Only the signal number can be
// made to match what the caller asked for.
TEXT ·sigactionTramp(SB),NOSPLIT,$32-0
	MOVQ	R9, 8(SP)		// token, before R9 is reused
	MOVL	SI, 24(SP)		// infostyle, before SI is reused
	MOVQ	R8, 16(SP)		// ctx, likewise
	MOVQ	DI, AX			// handler

	// Apple -> Linux signal number: the handler was installed under a
	// Linux number. SIGEMT and SIGINFO have no Linux number and keep
	// Apple's.
	MOVBLZX	DX, R9
	CMPL	R9, $32
	JAE	sigactiontramp_sig
	MOVQ	$·sigA2LTab(SB), R10
	MOVBLZX	(R10)(R9*1), R9
	CMPL	R9, $0
	JEQ	sigactiontramp_sig
	MOVL	R9, DX

sigactiontramp_sig:
	MOVL	DX, DI			// sig
	MOVQ	CX, SI			// info
	MOVQ	R8, DX			// ctx
	CALL	AX
	MOVQ	16(SP), DI		// ctx
	MOVL	24(SP), SI		// infostyle
	MOVQ	8(SP), DX		// token
	MOVL	$XNU_sigreturn, AX
	SYSCALL
	INT	$3			// sigreturn does not return
