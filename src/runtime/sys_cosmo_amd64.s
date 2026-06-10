// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

//
// System calls and other sys.stuff for AMD64, Cosmopolitan
// Supports both Linux (direct SYSCALL) and macOS (BSD syscall numbers with XNU prefix)
//

#include "go_asm.h"
#include "go_tls.h"
#include "textflag.h"
#include "cgo/abi_amd64.h"

#define AT_FDCWD -100

// Host OS indicators (must match os_cosmo_amd64.go)
#define HOSTXNU 8

// Linux AMD64 syscall numbers
#define SYS_read		0
#define SYS_write		1
#define SYS_close		3
#define SYS_mmap		9
#define SYS_munmap		11
#define SYS_brk 		12
#define SYS_rt_sigaction	13
#define SYS_rt_sigprocmask	14
#define SYS_rt_sigreturn	15
#define SYS_sched_yield 	24
#define SYS_mincore		27
#define SYS_madvise		28
#define SYS_nanosleep		35
#define SYS_setittimer		38
#define SYS_getpid		39
#define SYS_socket		41
#define SYS_connect		42
#define SYS_clone		56
#define SYS_exit		60
#define SYS_kill		62
#define SYS_sigaltstack 	131
#define SYS_arch_prctl		158
#define SYS_gettid		186
#define SYS_futex		202
#define SYS_sched_getaffinity	204
#define SYS_timer_create	222
#define SYS_timer_settime	223
#define SYS_timer_delete	226
#define SYS_clock_gettime	228
#define SYS_exit_group		231
#define SYS_tgkill		234
#define SYS_openat		257
#define SYS_faccessat		269
#define SYS_pipe2		293

// macOS/XNU BSD syscall numbers (with SYSCALL_CLASS_UNIX prefix 0x2000000)
// These are BSD syscall numbers + 0x2000000
#define XNU_exit		0x2000001	// BSD 1
#define XNU_read		0x2000003	// BSD 3
#define XNU_write		0x2000004	// BSD 4
#define XNU_open		0x2000005	// BSD 5
#define XNU_close		0x2000006	// BSD 6
#define XNU_getpid		0x2000014	// BSD 20
#define XNU_kill		0x2000025	// BSD 37
#define XNU_pipe		0x200002a	// BSD 42
#define XNU_sigaction		0x200002e	// BSD 46
#define XNU_sigprocmask		0x2000030	// BSD 48
#define XNU_sigaltstack		0x2000035	// BSD 53
#define XNU_gettimeofday	0x2000074	// BSD 116
#define XNU_munmap		0x2000049	// BSD 73
#define XNU_mprotect		0x200004a	// BSD 74
#define XNU_madvise		0x200004b	// BSD 75
#define XNU_mincore		0x200004e	// BSD 78
#define XNU_setitimer		0x2000053	// BSD 83
#define XNU_select		0x200005d	// BSD 93
#define XNU_socket		0x2000061	// BSD 97
#define XNU_connect		0x2000062	// BSD 98
#define XNU_mmap		0x20000c5	// BSD 197
#define XNU_sigreturn		0x20000b8	// BSD 184

// Helper macro: check if we're on macOS and jump to label if so
// Clobbers AX
#define CHECK_DARWIN(label) \
	MOVL	runtime·__hostos(SB), AX; \
	CMPL	AX, $HOSTXNU; \
	JEQ	label

TEXT runtime·exit(SB),NOSPLIT,$0-4
	CHECK_DARWIN(exit_darwin)
	// Linux path
	MOVL	code+0(FP), DI
	MOVL	$SYS_exit_group, AX
	SYSCALL
	RET
exit_darwin:
	MOVL	code+0(FP), DI
	MOVL	$XNU_exit, AX
	SYSCALL
	RET

// func exitThread(wait *atomic.Uint32)
TEXT runtime·exitThread(SB),NOSPLIT,$0-8
	MOVQ	wait+0(FP), AX
	// We're done using the stack.
	MOVL	$0, (AX)
	CHECK_DARWIN(exitThread_darwin)
	// Linux path
	MOVL	$0, DI	// exit code
	MOVL	$SYS_exit, AX
	SYSCALL
	// We may not even have a stack any more.
	INT	$3
	JMP	0(PC)
exitThread_darwin:
	MOVL	$0, DI
	MOVL	$XNU_exit, AX
	SYSCALL
	INT	$3
	JMP	0(PC)

TEXT runtime·open(SB),NOSPLIT,$0-20
	CHECK_DARWIN(open_darwin)
	// Linux path - use openat
	MOVL	$AT_FDCWD, DI
	MOVQ	name+0(FP), SI
	MOVL	mode+8(FP), DX
	MOVL	perm+12(FP), R10
	MOVL	$SYS_openat, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$-1, AX
	MOVL	AX, ret+16(FP)
	RET
open_darwin:
	// macOS path - use open directly
	MOVQ	name+0(FP), DI
	MOVL	mode+8(FP), SI
	MOVL	perm+12(FP), DX
	MOVL	$XNU_open, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	open_darwin_ok
	MOVL	$-1, AX
open_darwin_ok:
	MOVL	AX, ret+16(FP)
	RET

TEXT runtime·closefd(SB),NOSPLIT,$0-12
	CHECK_DARWIN(closefd_darwin)
	// Linux path
	MOVL	fd+0(FP), DI
	MOVL	$SYS_close, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$-1, AX
	MOVL	AX, ret+8(FP)
	RET
closefd_darwin:
	MOVL	fd+0(FP), DI
	MOVL	$XNU_close, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	closefd_darwin_ok
	MOVL	$-1, AX
closefd_darwin_ok:
	MOVL	AX, ret+8(FP)
	RET

TEXT runtime·write1(SB),NOSPLIT,$0-28
	CHECK_DARWIN(write1_darwin)
	// Linux path
	MOVQ	fd+0(FP), DI
	MOVQ	p+8(FP), SI
	MOVL	n+16(FP), DX
	MOVL	$SYS_write, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET
write1_darwin:
	MOVQ	fd+0(FP), DI
	MOVQ	p+8(FP), SI
	MOVL	n+16(FP), DX
	MOVL	$XNU_write, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET

TEXT runtime·read(SB),NOSPLIT,$0-28
	CHECK_DARWIN(read_darwin)
	// Linux path
	MOVL	fd+0(FP), DI
	MOVQ	p+8(FP), SI
	MOVL	n+16(FP), DX
	MOVL	$SYS_read, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET
read_darwin:
	MOVL	fd+0(FP), DI
	MOVQ	p+8(FP), SI
	MOVL	n+16(FP), DX
	MOVL	$XNU_read, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET

// func pipe2(flags int32) (r, w int32, errno int32)
TEXT runtime·pipe2(SB),NOSPLIT,$0-20
	CHECK_DARWIN(pipe2_darwin)
	// Linux path
	LEAQ	r+8(FP), DI
	MOVL	flags+0(FP), SI
	MOVL	$SYS_pipe2, AX
	SYSCALL
	MOVL	AX, errno+16(FP)
	RET
pipe2_darwin:
	// macOS pipe() doesn't support flags
	// pipe() returns r in AX, w in DX
	MOVL	$XNU_pipe, AX
	SYSCALL
	// On macOS, pipe() returns fds in AX (read) and DX (write) on success
	// On error, returns -1 in AX
	CMPQ	AX, $0xfffffffffffff001
	JLS	pipe2_darwin_ok
	MOVL	$-1, r+8(FP)
	MOVL	$-1, w+12(FP)
	NEGQ	AX
	MOVL	AX, errno+16(FP)
	RET
pipe2_darwin_ok:
	MOVL	AX, r+8(FP)
	MOVL	DX, w+12(FP)
	MOVL	$0, errno+16(FP)
	RET

TEXT runtime·usleep(SB),NOSPLIT,$24
	MOVL	$0, DX
	MOVL	usec+0(FP), AX
	MOVL	$1000000, CX
	DIVL	CX
	MOVQ	AX, 0(SP)	// seconds
	MOVL	$1000, AX	// usec to nsec
	MULL	DX
	MOVQ	AX, 8(SP)	// nanoseconds

	CHECK_DARWIN(usleep_darwin)
	// Linux path: nanosleep(&ts, 0)
	MOVQ	SP, DI
	MOVL	$0, SI
	MOVL	$SYS_nanosleep, AX
	SYSCALL
	RET
usleep_darwin:
	// macOS: use select with timeout
	// select(0, NULL, NULL, NULL, &tv)
	// Convert timespec to timeval (sec, usec not nsec)
	MOVQ	0(SP), DI	// seconds
	MOVQ	8(SP), SI	// nanoseconds
	MOVQ	$1000, AX
	XORQ	DX, DX
	DIVQ	AX		// SI = nsec / 1000 = usec
	MOVQ	DI, 0(SP)	// tv_sec
	MOVQ	SI, 8(SP)	// tv_usec
	MOVL	$0, DI		// nfds = 0
	MOVQ	$0, SI		// readfds = NULL
	MOVQ	$0, DX		// writefds = NULL
	MOVQ	$0, R10		// exceptfds = NULL
	LEAQ	0(SP), R8	// timeout
	MOVL	$XNU_select, AX
	SYSCALL
	RET

TEXT runtime·gettid(SB),NOSPLIT,$0-4
	CHECK_DARWIN(gettid_darwin)
	// Linux path
	MOVL	$SYS_gettid, AX
	SYSCALL
	MOVL	AX, ret+0(FP)
	RET
gettid_darwin:
	// macOS doesn't have gettid, use getpid as fallback
	MOVL	$XNU_getpid, AX
	SYSCALL
	MOVL	AX, ret+0(FP)
	RET

TEXT runtime·raise(SB),NOSPLIT,$0
	CHECK_DARWIN(raise_darwin)
	// Linux path
	MOVL	$SYS_getpid, AX
	SYSCALL
	MOVL	AX, R12
	MOVL	$SYS_gettid, AX
	SYSCALL
	MOVL	AX, SI	// arg 2 tid
	MOVL	R12, DI	// arg 1 pid
	MOVL	sig+0(FP), DX	// arg 3
	MOVL	$SYS_tgkill, AX
	SYSCALL
	RET
raise_darwin:
	// macOS: use kill(getpid(), sig)
	MOVL	$XNU_getpid, AX
	SYSCALL
	MOVL	AX, DI		// pid
	MOVL	sig+0(FP), SI	// sig
	MOVL	$XNU_kill, AX
	SYSCALL
	RET

TEXT runtime·raiseproc(SB),NOSPLIT,$0
	CHECK_DARWIN(raiseproc_darwin)
	// Linux path
	MOVL	$SYS_getpid, AX
	SYSCALL
	MOVL	AX, DI	// arg 1 pid
	MOVL	sig+0(FP), SI	// arg 2
	MOVL	$SYS_kill, AX
	SYSCALL
	RET
raiseproc_darwin:
	MOVL	$XNU_getpid, AX
	SYSCALL
	MOVL	AX, DI		// pid
	MOVL	sig+0(FP), SI	// sig
	MOVL	$XNU_kill, AX
	SYSCALL
	RET

TEXT ·getpid(SB),NOSPLIT,$0-8
	CHECK_DARWIN(getpid_darwin)
	// Linux path
	MOVL	$SYS_getpid, AX
	SYSCALL
	MOVQ	AX, ret+0(FP)
	RET
getpid_darwin:
	MOVL	$XNU_getpid, AX
	SYSCALL
	MOVQ	AX, ret+0(FP)
	RET

TEXT ·tgkill(SB),NOSPLIT,$0
	CHECK_DARWIN(tgkill_darwin)
	// Linux path
	MOVQ	tgid+0(FP), DI
	MOVQ	tid+8(FP), SI
	MOVQ	sig+16(FP), DX
	MOVL	$SYS_tgkill, AX
	SYSCALL
	RET
tgkill_darwin:
	// macOS: use kill instead (tgid is pid)
	MOVQ	tgid+0(FP), DI	// pid
	MOVQ	sig+16(FP), SI	// sig
	MOVL	$XNU_kill, AX
	SYSCALL
	RET

TEXT runtime·setitimer(SB),NOSPLIT,$0-24
	CHECK_DARWIN(setitimer_darwin)
	// Linux path
	MOVL	mode+0(FP), DI
	MOVQ	new+8(FP), SI
	MOVQ	old+16(FP), DX
	MOVL	$SYS_setittimer, AX
	SYSCALL
	RET
setitimer_darwin:
	MOVL	mode+0(FP), DI
	MOVQ	new+8(FP), SI
	MOVQ	old+16(FP), DX
	MOVL	$XNU_setitimer, AX
	SYSCALL
	RET

TEXT runtime·mincore(SB),NOSPLIT,$0-28
	CHECK_DARWIN(mincore_darwin)
	// Linux path
	MOVQ	addr+0(FP), DI
	MOVQ	n+8(FP), SI
	MOVQ	dst+16(FP), DX
	MOVL	$SYS_mincore, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET
mincore_darwin:
	MOVQ	addr+0(FP), DI
	MOVQ	n+8(FP), SI
	MOVQ	dst+16(FP), DX
	MOVL	$XNU_mincore, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET

// func nanotime1() int64
TEXT runtime·nanotime1(SB),NOSPLIT,$32-8
	MOVQ	SP, R12	// Save old SP; R12 unchanged by C code.

	MOVQ	g_m(R14), BX // BX unchanged by C code.

	// Set vdsoPC and vdsoSP for SIGPROF traceback.
	MOVQ	m_vdsoPC(BX), CX
	MOVQ	m_vdsoSP(BX), DX
	MOVQ	CX, 0(SP)
	MOVQ	DX, 8(SP)

	LEAQ	ret+0(FP), DX
	MOVQ	-8(DX), CX
	MOVQ	CX, m_vdsoPC(BX)
	MOVQ	DX, m_vdsoSP(BX)

	CMPQ	R14, m_curg(BX)	// Only switch if on curg.
	JNE	nanotime_noswitch

	MOVQ	m_g0(BX), DX
	MOVQ	(g_sched+gobuf_sp)(DX), SP	// Set SP to g0 stack

nanotime_noswitch:
	SUBQ	$32, SP		// Space for results
	ANDQ	$~15, SP	// Align for C code

	CHECK_DARWIN(nanotime1_darwin)
	// Linux path: Use clock_gettime directly
	MOVL	$1, DI // CLOCK_MONOTONIC
	LEAQ	16(SP), SI
	MOVQ	$SYS_clock_gettime, AX
	SYSCALL
	MOVQ	16(SP), AX	// sec
	MOVQ	24(SP), DX	// nsec
	JMP	nanotime_finish

nanotime1_darwin:
	// macOS: Use gettimeofday (returns sec, usec)
	LEAQ	16(SP), DI	// struct timeval *
	MOVQ	$0, SI		// timezone (NULL)
	MOVL	$XNU_gettimeofday, AX
	SYSCALL
	MOVQ	16(SP), AX	// tv_sec
	MOVQ	24(SP), DX	// tv_usec
	IMULQ	$1000, DX	// usec to nsec

nanotime_finish:
	MOVQ	R12, SP		// Restore real SP
	// Restore vdsoPC, vdsoSP
	MOVQ	8(SP), CX
	MOVQ	CX, m_vdsoSP(BX)
	MOVQ	0(SP), CX
	MOVQ	CX, m_vdsoPC(BX)
	// sec is in AX, nsec in DX
	// return nsec in AX
	IMULQ	$1000000000, AX
	ADDQ	DX, AX
	MOVQ	AX, ret+0(FP)
	RET

TEXT runtime·rtsigprocmask(SB),NOSPLIT,$0-28
	CHECK_DARWIN(rtsigprocmask_darwin)
	// Linux path
	MOVL	how+0(FP), DI
	MOVQ	new+8(FP), SI
	MOVQ	old+16(FP), DX
	MOVL	size+24(FP), R10
	MOVL	$SYS_rt_sigprocmask, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$0xf1, 0xf1  // crash
	RET
rtsigprocmask_darwin:
	// macOS sigprocmask doesn't have size parameter
	MOVL	how+0(FP), DI
	MOVQ	new+8(FP), SI
	MOVQ	old+16(FP), DX
	MOVL	$XNU_sigprocmask, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$0xf1, 0xf1  // crash
	RET

TEXT runtime·rt_sigaction(SB),NOSPLIT,$0-36
	CHECK_DARWIN(rt_sigaction_darwin)
	// Linux path
	MOVQ	sig+0(FP), DI
	MOVQ	new+8(FP), SI
	MOVQ	old+16(FP), DX
	MOVQ	size+24(FP), R10
	MOVL	$SYS_rt_sigaction, AX
	SYSCALL
	MOVL	AX, ret+32(FP)
	RET
rt_sigaction_darwin:
	// macOS sigaction has different structure
	// For now, return success without calling sigaction
	// TODO: Implement proper sigaction translation layer
	MOVL	$0, ret+32(FP)
	RET

TEXT runtime·sigfwd(SB),NOSPLIT,$0-32
	MOVQ	fn+0(FP),    AX
	MOVL	sig+8(FP),   DI
	MOVQ	info+16(FP), SI
	MOVQ	ctx+24(FP),  DX
	MOVQ	SP, BX		// callee-saved
	ANDQ	$~15, SP     // alignment for x86_64 ABI
	CALL	AX
	MOVQ	BX, SP
	RET

// Called using C ABI.
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME|NOFRAME,$0
	// Transition from C ABI to Go ABI.
	PUSH_REGS_HOST_TO_ABI0()

	// Set up ABIInternal environment: g in R14, cleared X15.
	get_tls(R12)
	MOVQ	g(R12), R14
	PXOR	X15, X15

	// Reserve space for spill slots.
	NOP	SP		// disable vet stack checking
	ADJSP   $24

	// Call into the Go signal handler
	MOVQ	DI, AX	// sig
	MOVQ	SI, BX	// info
	MOVQ	DX, CX	// ctx
	CALL	·sigtrampgo<ABIInternal>(SB)

	ADJSP	$-24

	POP_REGS_HOST_TO_ABI0()
	RET

// Used instead of sigtramp in programs that use cgo.
TEXT runtime·cgoSigtramp(SB),NOSPLIT,$0
	JMP	runtime·sigtramp(SB)

TEXT runtime·sigreturn__sigaction(SB),NOSPLIT,$0
	CHECK_DARWIN(sigreturn_darwin)
	MOVQ	$SYS_rt_sigreturn, AX
	SYSCALL
	INT $3	// not reached
sigreturn_darwin:
	MOVL	$XNU_sigreturn, AX
	SYSCALL
	INT $3	// not reached

// func mmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uint32) (p unsafe.Pointer, err int)
TEXT runtime·mmap(SB),NOSPLIT,$0
	CHECK_DARWIN(mmap_darwin)
	// Linux path
	MOVQ	addr+0(FP), DI
	MOVQ	n+8(FP), SI
	MOVL	prot+16(FP), DX
	MOVL	flags+20(FP), R10
	MOVL	fd+24(FP), R8
	MOVL	off+28(FP), R9

	MOVL	$SYS_mmap, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	mmap_ok
	NOTQ	AX
	INCQ	AX
	MOVQ	$0, p+32(FP)
	MOVQ	AX, err+40(FP)
	RET
mmap_ok:
	MOVQ	AX, p+32(FP)
	MOVQ	$0, err+40(FP)
	RET

mmap_darwin:
	// macOS mmap has same args but different syscall number
	// and MAP_ANONYMOUS is different (0x1000 instead of 0x20)
	MOVQ	addr+0(FP), DI
	MOVQ	n+8(FP), SI
	MOVL	prot+16(FP), DX
	MOVL	flags+20(FP), R10
	// Translate Linux flags to macOS flags
	// Linux MAP_ANONYMOUS = 0x20, macOS MAP_ANON = 0x1000
	MOVL	R10, AX
	ANDL	$0x20, AX	// Check for MAP_ANONYMOUS
	JZ	mmap_darwin_no_anon
	ANDL	$~0x20, R10	// Clear Linux MAP_ANONYMOUS
	ORL	$0x1000, R10	// Set macOS MAP_ANON
mmap_darwin_no_anon:
	MOVL	fd+24(FP), R8
	MOVL	off+28(FP), R9

	MOVL	$XNU_mmap, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	mmap_darwin_ok
	NOTQ	AX
	INCQ	AX
	MOVQ	$0, p+32(FP)
	MOVQ	AX, err+40(FP)
	RET
mmap_darwin_ok:
	MOVQ	AX, p+32(FP)
	MOVQ	$0, err+40(FP)
	RET

// func munmap(addr unsafe.Pointer, n uintptr)
TEXT runtime·munmap(SB),NOSPLIT,$0
	CHECK_DARWIN(munmap_darwin)
	// Linux path
	MOVQ	addr+0(FP), DI
	MOVQ	n+8(FP), SI
	MOVQ	$SYS_munmap, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$0xf1, 0xf1  // crash
	RET
munmap_darwin:
	MOVQ	addr+0(FP), DI
	MOVQ	n+8(FP), SI
	MOVL	$XNU_munmap, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$0xf1, 0xf1  // crash
	RET

// func walltime() (sec int64, nsec int32)
TEXT runtime·walltime(SB),NOSPLIT,$24-12
	CHECK_DARWIN(walltime_darwin)
	// Linux path
	MOVL	$0, DI // CLOCK_REALTIME
	LEAQ	0(SP), SI
	MOVQ	$SYS_clock_gettime, AX
	SYSCALL
	MOVQ	0(SP), AX	// sec
	MOVQ	8(SP), DX	// nsec
	MOVQ	AX, sec+0(FP)
	MOVL	DX, nsec+8(FP)
	RET
walltime_darwin:
	// macOS: Use gettimeofday
	LEAQ	0(SP), DI	// struct timeval *
	MOVQ	$0, SI		// timezone (NULL)
	MOVL	$XNU_gettimeofday, AX
	SYSCALL
	MOVQ	0(SP), AX	// tv_sec
	MOVQ	8(SP), DX	// tv_usec
	IMULQ	$1000, DX	// usec to nsec
	MOVQ	AX, sec+0(FP)
	MOVL	DX, nsec+8(FP)
	RET

TEXT runtime·madvise(SB),NOSPLIT,$0
	CHECK_DARWIN(madvise_darwin)
	// Linux path
	MOVQ	addr+0(FP), DI
	MOVQ	n+8(FP), SI
	MOVL	flags+16(FP), DX
	MOVQ	$SYS_madvise, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET
madvise_darwin:
	MOVQ	addr+0(FP), DI
	MOVQ	n+8(FP), SI
	MOVL	flags+16(FP), DX
	MOVL	$XNU_madvise, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET

// int64 futex(int32 *uaddr, int32 op, int32 val,
//	struct timespec *timeout, int32 *uaddr2, int32 val2);
TEXT runtime·futex(SB),NOSPLIT,$0
	CHECK_DARWIN(futex_darwin)
	// Linux path
	MOVQ	addr+0(FP), DI
	MOVL	op+8(FP), SI
	MOVL	val+12(FP), DX
	MOVQ	ts+16(FP), R10
	MOVQ	addr2+24(FP), R8
	MOVL	val3+32(FP), R9
	MOVL	$SYS_futex, AX
	SYSCALL
	MOVL	AX, ret+40(FP)
	RET
futex_darwin:
	// macOS doesn't have futex, return ENOSYS
	MOVL	$-38, AX	// ENOSYS
	MOVL	AX, ret+40(FP)
	RET

// int32 clone(int32 flags, void *stk, M *mp, G *gp, void (*fn)(void));
TEXT runtime·clone(SB),NOSPLIT|NOFRAME,$0
	CHECK_DARWIN(clone_darwin)
	// Linux path
	MOVL	flags+0(FP), DI
	MOVQ	stk+8(FP), SI
	MOVQ	$0, DX
	MOVQ	$0, R10
	MOVQ    $0, R8
	// Copy mp, gp, fn off parent stack for use by child.
	MOVQ	mp+16(FP), R13
	MOVQ	gp+24(FP), R9
	MOVQ	fn+32(FP), R12
	CMPQ	R13, $0    // m
	JEQ	nog1
	CMPQ	R9, $0    // g
	JEQ	nog1
	LEAQ	m_tls(R13), R8
	ADDQ	$8, R8	// ELF wants to use -8(FS)
	ORQ 	$0x00080000, DI //add flag CLONE_SETTLS(0x00080000) to call clone
nog1:
	MOVL	$SYS_clone, AX
	SYSCALL

	// In parent, return.
	CMPQ	AX, $0
	JEQ	3(PC)
	MOVL	AX, ret+40(FP)
	RET

	// In child, on new stack.
	MOVQ	SI, SP

	// If g or m are nil, skip Go-related setup.
	CMPQ	R13, $0    // m
	JEQ	nog2
	CMPQ	R9, $0    // g
	JEQ	nog2

	// Initialize m->procid to Linux tid
	MOVL	$SYS_gettid, AX
	SYSCALL
	MOVQ	AX, m_procid(R13)

	// In child, set up new stack
	get_tls(CX)
	MOVQ	R13, g_m(R9)
	MOVQ	R9, g(CX)
	MOVQ	R9, R14 // set g register
	CALL	runtime·stackcheck(SB)

nog2:
	// Call fn. This is the PC of an ABI0 function.
	CALL	R12

	// It shouldn't return. If it does, exit that thread.
	MOVL	$111, DI
	MOVL	$SYS_exit, AX
	SYSCALL
	JMP	-3(PC)	// keep exiting

clone_darwin:
	// macOS doesn't have clone, return error
	// Thread creation on macOS should use pthread_create
	// which requires cgo or a different approach
	MOVL	$-38, AX	// ENOSYS
	MOVL	AX, ret+40(FP)
	RET

TEXT runtime·sigaltstack(SB),NOSPLIT,$0
	CHECK_DARWIN(sigaltstack_darwin)
	// Linux path
	MOVQ	new+0(FP), DI
	MOVQ	old+8(FP), SI
	MOVQ	$SYS_sigaltstack, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$0xf1, 0xf1  // crash
	RET
sigaltstack_darwin:
	MOVQ	new+0(FP), DI
	MOVQ	old+8(FP), SI
	MOVL	$XNU_sigaltstack, AX
	SYSCALL
	// Don't crash on error, sigaltstack may fail on macOS
	RET

// set tls base to DI
TEXT runtime·settls(SB),NOSPLIT,$32
	CHECK_DARWIN(settls_darwin)
	// Linux path
	ADDQ	$8, DI	// ELF wants to use -8(FS)
	MOVQ	DI, SI
	MOVQ	$0x1002, DI	// ARCH_SET_FS
	MOVQ	$SYS_arch_prctl, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$0xf1, 0xf1  // crash
	RET
settls_darwin:
	// macOS x86_64 TLS is handled differently
	// Use thread_fast_set_cthread_self which is a Mach trap
	// For now, just return success - TLS may need different handling
	RET

TEXT runtime·osyield(SB),NOSPLIT,$0
	CHECK_DARWIN(osyield_darwin)
	// Linux path
	MOVL	$SYS_sched_yield, AX
	SYSCALL
	RET
osyield_darwin:
	// macOS: sched_yield is BSD syscall 331
	// Just return, no exact equivalent
	RET

TEXT runtime·sched_getaffinity(SB),NOSPLIT,$0
	CHECK_DARWIN(sched_getaffinity_darwin)
	// Linux path
	MOVQ	pid+0(FP), DI
	MOVQ	len+8(FP), SI
	MOVQ	buf+16(FP), DX
	MOVL	$SYS_sched_getaffinity, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET
sched_getaffinity_darwin:
	// macOS doesn't have sched_getaffinity, return error
	MOVL	$-38, AX	// ENOSYS
	MOVL	AX, ret+24(FP)
	RET

// int access(const char *name, int mode)
TEXT runtime·access(SB),NOSPLIT,$0
	CHECK_DARWIN(access_darwin)
	// Linux path - use faccessat
	MOVL	$AT_FDCWD, DI
	MOVQ	name+0(FP), SI
	MOVL	mode+8(FP), DX
	MOVL	$0, R10
	MOVL	$SYS_faccessat, AX
	SYSCALL
	MOVL	AX, ret+16(FP)
	RET
access_darwin:
	// macOS: use access directly (BSD syscall 33 = 0x2000021)
	MOVQ	name+0(FP), DI
	MOVL	mode+8(FP), SI
	MOVL	$0x2000021, AX	// XNU access
	SYSCALL
	MOVL	AX, ret+16(FP)
	RET

// int connect(int fd, const struct sockaddr *addr, socklen_t addrlen)
TEXT runtime·connect(SB),NOSPLIT,$0-28
	CHECK_DARWIN(connect_darwin)
	// Linux path
	MOVL	fd+0(FP), DI
	MOVQ	addr+8(FP), SI
	MOVL	len+16(FP), DX
	MOVL	$SYS_connect, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET
connect_darwin:
	MOVL	fd+0(FP), DI
	MOVQ	addr+8(FP), SI
	MOVL	len+16(FP), DX
	MOVL	$XNU_connect, AX
	SYSCALL
	MOVL	AX, ret+24(FP)
	RET

// int socket(int domain, int typ, int prot)
TEXT runtime·socket(SB),NOSPLIT,$0-20
	CHECK_DARWIN(socket_darwin)
	// Linux path
	MOVL	domain+0(FP), DI
	MOVL	typ+4(FP), SI
	MOVL	prot+8(FP), DX
	MOVL	$SYS_socket, AX
	SYSCALL
	MOVL	AX, ret+16(FP)
	RET
socket_darwin:
	MOVL	domain+0(FP), DI
	MOVL	typ+4(FP), SI
	MOVL	prot+8(FP), DX
	MOVL	$XNU_socket, AX
	SYSCALL
	MOVL	AX, ret+16(FP)
	RET

// func sbrk0() uintptr
TEXT runtime·sbrk0(SB),NOSPLIT,$0-8
	CHECK_DARWIN(sbrk0_darwin)
	// Linux path
	MOVL	$0, DI
	MOVL	$SYS_brk, AX
	SYSCALL
	MOVQ	AX, ret+0(FP)
	RET
sbrk0_darwin:
	// macOS doesn't have brk, return 0
	MOVQ	$0, ret+0(FP)
	RET
