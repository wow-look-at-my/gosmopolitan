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
#define HOSTWINDOWS 2
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

// Thread creation. Numbers from syscall/zsysnum_darwin_amd64.go; the ABI
// around them is Go's own pre-1.12 darwin port (sys_darwin_amd64.s), which
// created threads exactly this way before Go moved to libc.
#define XNU_bsdthread_create	0x2000168	// BSD 360
#define XNU_bsdthread_terminate	0x2000169	// BSD 361
#define XNU_bsdthread_register	0x200016e	// BSD 366
#define XNU_pthread_kill	0x2000148	// BSD 328 __pthread_kill

// Mach traps (class 0x1000000), numbered by osfmk/mach/syscall_sw.h.
#define MACH_thread_self	0x100001b	// thread_self_trap 27
#define MACH_swtch_pri		0x100003b	// swtch_pri 59

// PTHREAD_START_CUSTOM: the caller supplies the stack, which is the only
// mode that makes sense for a runtime that already allocated a g0 stack.
#define PTHREAD_START_CUSTOM	0x01000000

// thread_fast_set_cthread_self, a machdep call (class 0x3000000), is how
// x86-64 XNU sets a thread's GS base. The kernel stores the value it is
// given as the base, unchanged (machine_thread_set_tsd_base).
#define XNU_set_cthread_self	0x3000003

// Helper macro: check if we're on macOS and jump to label if so
// Clobbers AX
#define CHECK_DARWIN(label) \
	MOVL	runtime·__hostos(SB), AX; \
	CMPL	AX, $HOSTXNU; \
	JEQ	label

// Helper macro: check if we're on Windows NT and jump to label if so.
// Checked BEFORE the Linux fallthrough at every insertion point: no raw
// SYSCALL may execute when __hostos == HOSTWINDOWS.
// Clobbers AX
#define CHECK_WINDOWS(label) \
	MOVL	runtime·__hostos(SB), AX; \
	CMPL	AX, $HOSTWINDOWS; \
	JEQ	label

// NT branches below call win64 functions resolved into runtime·nt*Fn
// variables at osArchInit (os_cosmo_nt.go). Win64 call discipline for a
// Go asm function body (entry SP == 8 mod 16): SUBQ $40, SP realigns to
// 16 and provides the 32-byte shadow space; args in CX, DX, R8, R9.

TEXT runtime·exit(SB),NOSPLIT,$0-4
	CHECK_WINDOWS(exit_nt)
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
exit_nt:
	// Chunk D2: exit must take ntSuspendLock before ExitProcess so a
	// SuspendThread from ntPreemptM can never be mid-flight while the
	// process dies (the suspender-killed-mid-suspend wedge, upstream
	// os_windows.go exit()). Tail JMP to the Go-side ntExit
	// (os_cosmo_nt_preempt.go): same signature, FP slot carries over
	// (the ntwrite1tramp discipline); ntExit performs the ExitProcess
	// through ntcall, which realigns per the win64 rules.
	JMP	runtime·ntExit(SB)

// func exitThread(wait *atomic.Uint32)
TEXT runtime·exitThread(SB),NOSPLIT,$0-8
	MOVQ	wait+0(FP), AX
	// We're done using the stack.
	MOVL	$0, (AX)
	CHECK_WINDOWS(exitThread_nt)
	CHECK_DARWIN(exitThread_darwin)
	// Linux path
	MOVL	$0, DI	// exit code
	MOVL	$SYS_exit, AX
	SYSCALL
	// We may not even have a stack any more.
	INT	$3
	JMP	0(PC)
exitThread_darwin:
	// bsdthread_terminate(stackaddr, freesize, port, sem) ends THIS
	// thread; XNU exit would end the process. The runtime owns the
	// stack, so nothing is freed and no port or semaphore is signaled.
	MOVQ	$0, DI
	MOVQ	$0, SI
	MOVQ	$0, DX
	MOVQ	$0, R10
	MOVL	$XNU_bsdthread_terminate, AX
	SYSCALL
	INT	$3
	JMP	0(PC)
exitThread_nt:
	// ExitThread(0). Direct win64 call; *wait was already cleared, so
	// our stack may be freed underneath us - no Go calls from here.
	// Realign like exit_nt: win64 needs entry SP == 8 (mod 16).
	MOVQ	runtime·ntExitThreadFn(SB), AX
	XORL	CX, CX
	ANDQ	$~15, SP	// 16-align: CALL leaves entry SP == 8 (mod 16)
	SUBQ	$32, SP		// shadow space
	CALL	AX
	INT	$3	// not reached

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
	// macOS path - use open directly. XNU sets the carry flag on
	// failure and returns a positive errno; the Linux -4096 test this
	// used to apply read ENOENT (2) as a successful open on fd 2.
	MOVQ	name+0(FP), DI
	MOVL	mode+8(FP), SI
	MOVL	perm+12(FP), DX
	MOVL	$XNU_open, AX
	SYSCALL
	JCS	open_darwin_err
	MOVL	AX, ret+16(FP)
	RET
open_darwin_err:
	MOVL	$-1, ret+16(FP)
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
	JCS	closefd_darwin_err
	MOVL	AX, ret+8(FP)
	RET
closefd_darwin_err:
	MOVL	$-1, ret+8(FP)
	RET

TEXT runtime·write1(SB),NOSPLIT,$0-28
	CHECK_WINDOWS(write1_nt)
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
	// Callers read a negative result as -errno, so a failure must not
	// come back as XNU's positive Apple errno - that reads as a short
	// write of that many bytes.
	MOVQ	fd+0(FP), DI
	MOVQ	p+8(FP), SI
	MOVL	n+16(FP), DX
	MOVL	$XNU_write, AX
	SYSCALL
	JCS	write1_darwin_err
	MOVL	AX, ret+24(FP)
	RET
write1_darwin_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	NEGQ	AX
	MOVL	AX, ret+24(FP)
	RET
write1_nt:
	// Tail call the framed trampoline (sys_cosmo_nt_amd64.s), which
	// calls the Go-side ntwrite1 (WriteFile via ntcall6/asmcgocall).
	// Same signature, so the FP argument slots carry over.
	JMP	runtime·ntwrite1tramp(SB)

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
	// -errno on failure, like write1_darwin above.
	MOVL	fd+0(FP), DI
	MOVQ	p+8(FP), SI
	MOVL	n+16(FP), DX
	MOVL	$XNU_read, AX
	SYSCALL
	JCS	read_darwin_err
	MOVL	AX, ret+24(FP)
	RET
read_darwin_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	NEGQ	AX
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
	// On success pipe() returns the read fd in AX and the write fd in
	// DX. On failure the carry flag is set and AX holds a positive
	// Apple errno, which the old -4096 test accepted as a pair of fds.
	JCS	pipe2_darwin_err
	MOVL	AX, r+8(FP)
	MOVL	DX, w+12(FP)
	MOVL	$0, errno+16(FP)
	RET
pipe2_darwin_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	MOVL	$-1, r+8(FP)
	MOVL	$-1, w+12(FP)
	NEGQ	AX
	MOVL	AX, errno+16(FP)
	RET

TEXT runtime·usleep(SB),NOSPLIT,$24
	CHECK_WINDOWS(usleep_nt)
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
	// Convert the timespec at 0(SP)/8(SP) into a timeval in place:
	// tv_usec = nsec / 1000. (This used to compute DIVQ AX with AX
	// as BOTH dividend setup and divisor - dividing the constant 1000
	// by itself - and then stored the raw nanosecond count as tv_usec,
	// an invalid timeval whenever nsec >= 1e6.)
	MOVQ	8(SP), AX	// nanoseconds
	XORQ	DX, DX
	MOVQ	$1000, CX
	DIVQ	CX		// AX = nsec / 1000 = usec
	MOVQ	AX, 8(SP)	// tv_usec (tv_sec already at 0(SP))
	MOVL	$0, DI		// nfds = 0
	MOVQ	$0, SI		// readfds = NULL
	MOVQ	$0, DX		// writefds = NULL
	MOVQ	$0, R10		// exceptfds = NULL
	LEAQ	0(SP), R8	// timeout
	MOVL	$XNU_select, AX
	SYSCALL
	RET
usleep_nt:
	// Sleep(ms), ms = ceil(usec/1000): any nonzero request sleeps at
	// least 1ms. Direct win64 call (1 arg, nosplit context). Go
	// stacks are only 8-aligned: save SP in SI (win64 callee-saved)
	// and realign so the callee sees entry SP == 8 (mod 16) - same
	// fix as exit_nt.
	MOVL	usec+0(FP), AX
	ADDL	$999, AX
	XORL	DX, DX
	MOVL	$1000, CX
	DIVL	CX
	MOVL	AX, CX
	MOVQ	runtime·ntSleepFn(SB), AX
	MOVQ	SP, SI
	ANDQ	$~15, SP	// 16-align: CALL leaves entry SP == 8 (mod 16)
	SUBQ	$32, SP		// shadow space
	CALL	AX
	MOVQ	SI, SP
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
	CHECK_WINDOWS(raise_nt)
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
	// kill(getpid(), sig, posix=1) with the APPLE signal number. A
	// signal with no Apple number is dropped: the table answers 0, and
	// kill(pid, 0) is an existence probe.
	MOVL	sig+0(FP), SI
	CMPL	SI, $65
	JAE	raise_darwin_drop
	MOVQ	$runtime·cosmoSigL2ATab(SB), R11
	MOVBLZX	(R11)(SI*1), SI
	CMPL	SI, $0
	JEQ	raise_darwin_drop
	MOVL	$XNU_getpid, AX
	SYSCALL
	MOVL	AX, DI		// pid
	MOVL	$1, DX		// posix
	MOVL	$XNU_kill, AX
	SYSCALL
raise_darwin_drop:
	RET
raise_nt:
	// NT (chunk D1): raise is only called on paths that expect the
	// process to die of the signal (dieFromSignal, raisebadsignal;
	// delivery-to-handler decisions happen before raise is reached,
	// ntKillSelf). Exit with the fork's encoded signal-death status
	// so wait4 reports "killed by signal". Tail JMP: same signature,
	// FP slot carries over.
	JMP	runtime·ntExitEncoded(SB)

TEXT runtime·raiseproc(SB),NOSPLIT,$0
	CHECK_WINDOWS(raiseproc_nt)
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
	// Same translation as raise_darwin.
	MOVL	sig+0(FP), SI
	CMPL	SI, $65
	JAE	raiseproc_darwin_drop
	MOVQ	$runtime·cosmoSigL2ATab(SB), R11
	MOVBLZX	(R11)(SI*1), SI
	CMPL	SI, $0
	JEQ	raiseproc_darwin_drop
	MOVL	$XNU_getpid, AX
	SYSCALL
	MOVL	AX, DI		// pid
	MOVL	$1, DX		// posix
	MOVL	$XNU_kill, AX
	SYSCALL
raiseproc_darwin_drop:
	RET
raiseproc_nt:
	// NT (chunk D1): same as raise_nt - a process-directed fatal
	// signal (sighandler's crash relay) kills this process with the
	// encoded status.
	JMP	runtime·ntExitEncoded(SB)

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
	CHECK_WINDOWS(tgkill_nt)
	CHECK_DARWIN(tgkill_darwin)
	// Linux path
	MOVQ	tgid+0(FP), DI
	MOVQ	tid+8(FP), SI
	MOVQ	sig+16(FP), DX
	MOVL	$SYS_tgkill, AX
	SYSCALL
	RET
tgkill_darwin:
	// __pthread_kill(thread_port, sig): tid is the mach port m.procid
	// holds (minitProcid), sig becomes the APPLE number. A signal with
	// no Apple number is dropped.
	MOVQ	sig+16(FP), SI
	CMPQ	SI, $65
	JAE	tgkill_darwin_drop
	MOVQ	$runtime·cosmoSigL2ATab(SB), R11
	MOVBLZX	(R11)(SI*1), SI
	CMPL	SI, $0
	JEQ	tgkill_darwin_drop
	MOVQ	tid+8(FP), DI	// mach port
	MOVL	$XNU_pthread_kill, AX
	SYSCALL
tgkill_darwin_drop:
	RET
tgkill_nt:
	// NT wave 1: signal sends are dropped (signalM is also gated in
	// Go; this is the belt-and-braces asm gate).
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
	JCS	mincore_darwin_err
	MOVL	AX, ret+24(FP)
	RET
mincore_darwin_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	NEGQ	AX
	MOVL	AX, ret+24(FP)
	RET

// func nanotime1() int64
TEXT runtime·nanotime1(SB),NOSPLIT,$32-8
	CHECK_WINDOWS(nanotime1_nt)
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
	// Post-Sierra XNU gettimeofday takes a THIRD argument: a
	// uint64* the kernel copies mach_absolute_time into when
	// non-NULL. Leaving caller garbage in DX made the kernel write
	// 8 bytes through a random pointer. Zero it.
	XORL	DX, DX
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

nanotime1_nt:
	// KUSER_SHARED_DATA InterruptTime, ported from upstream
	// time_windows_amd64.s / time_windows.h: on amd64 the lo (u32,
	// +0) and hi1 (u32, +4) halves are read as one atomic 64-bit
	// load, so the 386-style hi1/lo/hi2 coherence loop is not
	// needed. Units are 100ns. No vdsoPC/vdsoSP bookkeeping: this
	// is a plain memory read, not a call.
	MOVQ	$0x7ffe0008, CX	// _INTERRUPT_TIME
	MOVQ	0(CX), AX
	IMULQ	$100, AX
	MOVQ	AX, ret+0(FP)
	RET

TEXT runtime·rtsigprocmask(SB),NOSPLIT,$0-28
	// NT: no signal machinery in wave 1; fake success BEFORE the
	// crash-on-failure checks below (mirrors rt_sigaction's darwin
	// return-0 stub idiom).
	CHECK_WINDOWS(rtsigprocmask_nt)
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
	// Unreachable: sigprocmask routes darwin hosts through
	// darwinSigprocmask (signal_cosmo_xnu_amd64.go), which translates
	// the mask as well as `how`. This branch translated `how` alone and
	// handed the kernel the 8-byte Linux mask untouched, so every mask
	// it set named the wrong signals.
	//
	// Crash rather than lie if a new caller reaches the asm directly.
	MOVL	$0xf3, 0xf3
	RET
rtsigprocmask_nt:
	// Unreachable: sigprocmask routes NT hosts through ntSigprocmask,
	// which keeps the mask the self-delivery path consults. This used to
	// return success while blocking nothing, so a critical section that
	// had just masked every signal could still be reentered by one.
	MOVL	$0xf5, 0xf5

TEXT runtime·rt_sigaction(SB),NOSPLIT,$0-36
	CHECK_WINDOWS(rt_sigaction_nt)
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
	// Unreachable: sysSigaction routes darwin hosts through
	// darwinSigaction (signal_cosmo_xnu_amd64.go), which translates the
	// struct and issues __sigaction with a real trampoline. This used to
	// return success without installing anything, so every handler the
	// runtime thought it had set was absent.
	//
	// Crash rather than lie if a new caller reaches the asm directly.
	MOVL	$0xf3, 0xf3
	MOVL	$0, ret+32(FP)
	RET
rt_sigaction_nt:
	// Unreachable: sysSigaction routes NT hosts through ntSigaction
	// (os_cosmo_nt_sig.go), which records the handler the self-delivery
	// path then consults. This used to return success without recording
	// anything, which is the same lie the darwin stub above told.
	//
	// Crash rather than lie if a new caller reaches the asm directly.
	MOVL	$0xf4, 0xf4

TEXT runtime·sigfwd(SB),NOSPLIT,$0-32
	MOVL	sig+8(FP),   DI
	CHECK_DARWIN(sigfwd_darwin)
sigfwd_call:
	MOVQ	fn+0(FP),    AX
	MOVQ	info+16(FP), SI
	MOVQ	ctx+24(FP),  DX
	MOVQ	SP, BX		// callee-saved
	ANDQ	$~15, SP     // alignment for x86_64 ABI
	CALL	AX
	MOVQ	BX, SP
	RET
sigfwd_darwin:
	// A forwarded handler is foreign code that expects the host's ABI.
	// info and ctx are Apple-native already, so hand it the APPLE signal
	// number too. An unmapped number cannot get here: no handler could
	// have been installed for it.
	CMPL	DI, $65
	JAE	sigfwd_call
	MOVQ	$runtime·cosmoSigL2ATab(SB), R11
	MOVBLZX	(R11)(DI*1), DI
	JMP	sigfwd_call

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

// cosmoXnuSigtramp is the sa_tramp of a raw __sigaction
// (signal_cosmo_xnu_amd64.go). The KERNEL enters it, not Go, with the
// register state sendsig (XNU bsd/dev/i386/unix_signal.c) builds:
//
//	DI  handler (ignored - sigtrampgo dispatches by signal)
//	SI  infostyle, which sigreturn needs back
//	DX  sig, APPLE numbering
//	CX  info
//	R8  ctx
//	R9  token, which sigreturn needs back
//
// sigreturn(uctx, infostyle, token): the kernel refuses a call whose
// token does not match the one it handed out.
//
// arm64 needs none of this: Apple libc supplies its own trampoline
// there, and the Syslib hands it the already-installed handler. A raw
// caller owns both ends - entering the handler and calling sigreturn
// when it comes back.
//
// The signal number becomes the Linux one before Go sees it; SIGEMT and
// SIGINFO have no Linux number and skip the handler. The reshuffle lands
// on the (sig, info, ctx) contract runtime·sigtramp already implements,
// so the C-to-Go transition is not written twice.
TEXT runtime·cosmoXnuSigtramp(SB),NOSPLIT,$32
	MOVQ	R9, 8(SP)		// token
	MOVQ	R8, 16(SP)		// ctx
	MOVL	SI, 24(SP)		// infostyle
	MOVL	DX, DX			// sig is an int: drop the upper half
	CMPL	DX, $32
	JAE	cosmoXnuSigtramp_ret	// out of table: no Linux meaning
	MOVQ	$runtime·cosmoSigA2LTab(SB), R11
	MOVBLZX	(R11)(DX*1), DI		// sig, Linux numbering
	CMPL	DI, $0
	JEQ	cosmoXnuSigtramp_ret	// SIGEMT/SIGINFO: no Linux number
	MOVQ	CX, SI			// info
	MOVQ	R8, DX			// ctx
	CALL	runtime·sigtramp(SB)
cosmoXnuSigtramp_ret:
	MOVQ	16(SP), DI		// ctx
	MOVL	24(SP), SI		// infostyle
	MOVQ	8(SP), DX		// token
	MOVL	$XNU_sigreturn, AX
	SYSCALL
	INT	$3			// sigreturn does not return

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
	// The carry flag decides. A failed mmap used to come back as a
	// mapping at address ENOMEM (12), which every caller then wrote to.
	JCS	mmap_darwin_err
	MOVQ	AX, p+32(FP)
	MOVQ	$0, err+40(FP)
	RET
mmap_darwin_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	MOVQ	$0, p+32(FP)
	MOVQ	AX, err+40(FP)
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
	JCC	2(PC)
	MOVL	$0xf1, 0xf1  // crash
	RET

// func walltime() (sec int64, nsec int32)
TEXT runtime·walltime(SB),NOSPLIT,$24-12
	CHECK_WINDOWS(walltime_nt)
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
	// Zero the third argument (mach_absolute_time out-pointer on
	// post-Sierra XNU); see nanotime1_darwin.
	XORL	DX, DX
	MOVL	$XNU_gettimeofday, AX
	SYSCALL
	MOVQ	0(SP), AX	// tv_sec
	MOVQ	8(SP), DX	// tv_usec
	IMULQ	$1000, DX	// usec to nsec
	MOVQ	AX, sec+0(FP)
	MOVL	DX, nsec+8(FP)
	RET
walltime_nt:
	// KUSER_SHARED_DATA SystemTime (see nanotime1_nt for the atomic
	// 64-bit read). Units are 100ns since 1601-01-01 (FILETIME
	// epoch); subtract 116444736000000000 to rebase onto the unix
	// epoch, then split into sec and nsec.
	MOVQ	$0x7ffe0014, CX	// _SYSTEM_TIME
	MOVQ	0(CX), AX
	MOVQ	$116444736000000000, CX
	SUBQ	CX, AX
	XORL	DX, DX
	MOVQ	$10000000, CX	// 100ns units per second
	DIVQ	CX		// AX = sec, DX = remainder (100ns units)
	IMULQ	$100, DX	// -> nsec
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
	JCS	madvise_darwin_err
	MOVL	AX, ret+24(FP)
	RET
madvise_darwin_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	NEGQ	AX
	MOVL	AX, ret+24(FP)
	RET

// int64 futex(int32 *uaddr, int32 op, int32 val,
//	struct timespec *timeout, int32 *uaddr2, int32 val2);
TEXT runtime·futex(SB),NOSPLIT,$0
	// NT is unreachable here (futexsleep/futexwakeup dispatch to
	// WaitOnAddress/WakeByAddressSingle in Go first); ENOSYS is the
	// belt-and-braces answer if a new caller slips through.
	CHECK_WINDOWS(futex_darwin)
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
	// The kernel's CLONE_SETTLS can only set FS on x86-64; the cosmo
	// TLS model is gs:0x28 (see settls), so the child installs its own
	// GS base below instead.
	MOVQ	mp+16(FP), R13
	MOVQ	gp+24(FP), R9
	MOVQ	fn+32(FP), R12
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

	// Install TLS before any instruction touches g: point the GS base
	// at &m.tls[0]-0x28 so gs:0x28 (the g slot) addresses m.tls[0].
	// Mirrors the Linux branch of settls.
	LEAQ	m_tls(R13), SI
	SUBQ	$0x28, SI
	MOVQ	$0x1001, DI	// ARCH_SET_GS
	MOVQ	$SYS_arch_prctl, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$0xf1, 0xf1  // crash

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

// XNU has no clone. It has bsdthread_create, which is what Go's own
// darwin port used until it moved to libc in Go 1.12, and the ABI below
// is that port's (go1.8 runtime/sys_darwin_amd64.s).
//
// bsdthread_create(fn, arg, stack, pthread, flags). The kernel relays
// arg1 into DX and arg2 into CX in the new thread, so only TWO values
// reach the child. gp is therefore not passed: newosproc always passes
// mp.g0, and cosmoBsdthreadStart derives it from the m, exactly as Go
// did. newosproc0 passes a nil m and the stub skips the g setup, which
// is the nog2 case on the Linux side.
//
// The stub must be registered before the first create; osArchInit does
// that (os_cosmo_amd64.go), and an unregistered create fails rather than
// starting a thread at an address the kernel does not know.
clone_darwin:
	MOVQ	fn+32(FP), DI		// relayed to the child in DX
	MOVQ	mp+16(FP), SI		// relayed to the child in CX
	MOVQ	stk+8(FP), DX
	MOVQ	$0, R10			// pthread: none, we own the stack
	MOVQ	$PTHREAD_START_CUSTOM, R8
	MOVQ	$0, R9
	MOVL	$XNU_bsdthread_create, AX
	SYSCALL
	JCS	clone_darwin_err
	MOVL	$0, ret+40(FP)
	RET
clone_darwin_err:
	// newosproc reads a negative return as -errno and retries EAGAIN, so
	// the Apple number has to become the Linux one before the sign flip.
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	NEGQ	AX
	MOVL	AX, ret+40(FP)
	RET

// cosmoBsdthreadStart is where the kernel enters a thread made by
// bsdthread_create. It is not called; it is jumped to with a register
// state the kernel chooses:
//
//	DI  pthread (unused - we passed none)
//	SI  mach port for this thread
//	DX  arg1 of the create call, our fn
//	CX  arg2 of the create call, our m
//	R8  stack top
//	R9  flags
//
// SP arrives 128 bytes below the stack top, so the first move is to take
// the stack the caller actually allocated.
TEXT runtime·cosmoBsdthreadStart(SB),NOSPLIT,$0
	MOVQ	R8, SP
	CMPQ	CX, $0
	JEQ	bsdthread_start_nog

	// settls takes &m.tls[0] in DI and points the GS base 0x28 below it,
	// so gs:0x28 addresses m.tls[0] - the same slot g lives in on Linux.
	PUSHQ	DX
	PUSHQ	CX
	PUSHQ	SI
	LEAQ	m_tls(CX), DI
	CALL	runtime·settls(SB)
	POPQ	SI
	POPQ	CX
	POPQ	DX

	MOVQ	SI, m_procid(CX)	// the mach port is this thread's id
	MOVQ	m_g0(CX), AX
	MOVQ	CX, g_m(AX)
	get_tls(BX)
	MOVQ	AX, g(BX)
	MOVQ	AX, R14			// g register, for ABIInternal callees
	CALL	runtime·stackcheck(SB)

bsdthread_start_nog:
	CALL	DX			// fn, an ABI0 entry (mstart)

	// fn is not supposed to return. If it does, end this thread rather
	// than the process: every other thread is still running.
	MOVQ	$0, DI			// stack to free: none, the runtime owns it
	MOVQ	$0, SI
	MOVQ	$0, DX
	MOVQ	$0, R10
	MOVL	$XNU_bsdthread_terminate, AX
	SYSCALL
	MOVL	$0xf2, 0xf2		// crash: bsdthread_terminate returned
	RET

// func cosmoBsdthreadRegister() int32
//
// Tells the kernel which address to enter new threads at. Must run once,
// before any bsdthread_create. Returns 0, or a LINUX errno.
TEXT runtime·cosmoBsdthreadRegister(SB),NOSPLIT,$0-4
	MOVQ	$runtime·cosmoBsdthreadStart(SB), DI
	MOVQ	$0, SI			// no workqueue thread entry
	MOVQ	$0, DX
	MOVQ	$0, R10
	MOVQ	$0, R8
	MOVQ	$0, R9
	MOVL	$XNU_bsdthread_register, AX
	SYSCALL
	JCS	bsdthread_register_err
	MOVL	$0, ret+0(FP)
	RET
bsdthread_register_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	MOVL	AX, ret+0(FP)
	RET

// sigaltstack itself is a Go dispatcher (signal_cosmo_xnu_amd64.go):
// XNU hosts go to darwinSigaltstack, which translates the struct and
// the flags.
TEXT runtime·sigaltstackLinux(SB),NOSPLIT,$0
	// NT: no signal machinery in wave 1; fake success BEFORE the
	// crash-on-failure check below.
	CHECK_WINDOWS(sigaltstack_nt)
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
	// Unreachable: the Linux stackt handed raw to XNU has its size and
	// flags swapped. Crash rather than lie if a new caller reaches the
	// asm directly.
	MOVL	$0xf3, 0xf3
	RET
sigaltstack_nt:
	RET

// set tls base to DI
//
// Cosmo amd64 TLS model: g lives at gs:0x28 (Tlsoffset 0x28, segment
// prefix GS - see cmd/link/internal/ld/sym.go and
// cmd/internal/obj/x86/asm6.go). On Windows hosts gs:0x28 is the TEB
// ArbitraryUserPointer slot, so there is nothing to set up; on Linux
// hosts point the GS base at &m.tls[0]-0x28 so that gs:0x28 addresses
// m.tls[0], the slot g has always lived in.
TEXT runtime·settls(SB),NOSPLIT,$32
	// Windows: the TEB already backs gs:0x28; no setup syscall exists
	// or is needed.
	CHECK_WINDOWS(settls_nt)
	CHECK_DARWIN(settls_darwin)
	// Linux path
	SUBQ	$0x28, DI	// gs:0x28 must address m.tls[0]
	MOVQ	DI, SI
	MOVQ	$0x1001, DI	// ARCH_SET_GS
	MOVQ	$SYS_arch_prctl, AX
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001
	JLS	2(PC)
	MOVL	$0xf1, 0xf1  // crash
	RET
settls_nt:
	RET
settls_darwin:
	// DI is &m.tls[0]. The kernel installs the value as the GS base
	// unchanged, so pass &m.tls[0]-0x28 and gs:0x28 addresses the slot.
	SUBQ	$0x28, DI
	MOVL	$XNU_set_cthread_self, AX
	SYSCALL
	RET

// func cosmoMachThreadSelf() uint32
//
// thread_self_trap: this thread's mach port name, which __pthread_kill
// takes. A mach trap returns its result in AX with no carry flag.
TEXT runtime·cosmoMachThreadSelf(SB),NOSPLIT,$0-4
	MOVL	$MACH_thread_self, AX
	SYSCALL
	MOVL	AX, ret+0(FP)
	RET

// func cosmoXlatErrno(e uint32) uint32
//
// Go-callable wrapper over cosmo_xlat_errno_ax, so a test can pin the
// table.
TEXT runtime·cosmoXlatErrno(SB),NOSPLIT,$0-12
	MOVL	e+0(FP), AX
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	MOVL	AX, ret+8(FP)
	RET

TEXT runtime·osyield(SB),NOSPLIT,$0
	CHECK_WINDOWS(osyield_nt)
	CHECK_DARWIN(osyield_darwin)
	// Linux path
	MOVL	$SYS_sched_yield, AX
	SYSCALL
	RET
osyield_darwin:
	// swtch_pri(0), the mach trap Apple libc's sched_yield issues. BSD
	// 331 is __disable_threadsignal, not a yield.
	MOVL	$0, DI
	MOVL	$MACH_swtch_pri, AX
	SYSCALL
	RET
osyield_nt:
	// Sleep(0) yields to any ready thread. Direct win64 call;
	// realign like usleep_nt (win64 wants entry SP == 8 mod 16).
	MOVQ	runtime·ntSleepFn(SB), AX
	XORL	CX, CX
	MOVQ	SP, SI
	ANDQ	$~15, SP	// 16-align: CALL leaves entry SP == 8 (mod 16)
	SUBQ	$32, SP		// shadow space
	CALL	AX
	MOVQ	SI, SP
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
	JCS	access_darwin_err
	MOVL	AX, ret+16(FP)
	RET
access_darwin_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	NEGQ	AX
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
	JCS	connect_darwin_err
	MOVL	AX, ret+24(FP)
	RET
connect_darwin_err:
	// The caller compares against -EINPROGRESS, so the Apple number
	// has to become the Linux one before the sign flip.
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	NEGQ	AX
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
	JCS	socket_darwin_err
	MOVL	AX, ret+16(FP)
	RET
socket_darwin_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	NEGQ	AX
	MOVL	AX, ret+16(FP)
	RET

// func sbrk0() uintptr
TEXT runtime·sbrk0(SB),NOSPLIT,$0-8
	// NT has no brk; 0 means "not implemented" to mallocinit. This IS
	// on the NT boot path (mallocinit queries the brk), so the gate is
	// mandatory.
	CHECK_WINDOWS(sbrk0_darwin)
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

// func cosmoXnuSyscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1 uintptr, errno int32)
//
// A raw XNU syscall by BSD number (caller supplies the 0x2000000 class
// prefix), for the darwin calls that have no Linux number to dispatch on
// - kqueue and kevent. errno is 0 on success and a LINUX errno on
// failure; r1 is meaningless unless errno is 0.
//
// Refuses to issue anything on a non-XNU host: this is the one entry
// here a caller reaches by naming an Apple syscall directly, so a
// mis-gated caller must get ENOSYS rather than run BSD number 362 as
// whatever Linux calls 362.
TEXT runtime·cosmoXnuSyscall6(SB),NOSPLIT,$0-68
	MOVL	runtime·__hostos(SB), R11
	CMPL	R11, $HOSTXNU
	JNE	cosmoXnuSyscall6_enosys
	MOVQ	a1+8(FP), DI
	MOVQ	a2+16(FP), SI
	MOVQ	a3+24(FP), DX
	MOVQ	a4+32(FP), R10
	MOVQ	a5+40(FP), R8
	MOVQ	a6+48(FP), R9
	MOVQ	num+0(FP), AX
	SYSCALL
	JCS	cosmoXnuSyscall6_err
	MOVQ	AX, r1+56(FP)
	MOVL	$0, errno+64(FP)
	RET
cosmoXnuSyscall6_err:
	CALL	runtime·cosmo_xlat_errno_ax(SB)
	MOVQ	$0, r1+56(FP)
	MOVL	AX, errno+64(FP)
	RET
cosmoXnuSyscall6_enosys:
	MOVQ	$0, r1+56(FP)
	MOVL	$38, errno+64(FP)	// ENOSYS
	RET

// runtime·cosmo_xlat_errno_ax translates a positive Apple errno in AX into
// the corresponding positive Linux errno in AX. A value above 106 passes
// through unchanged. Leaf; clobbers only R11, so any darwin return path can
// CALL it.
//
// XNU reports failure by setting the carry flag and returning a POSITIVE
// APPLE errno, while Go compares against LINUX values (Errno, EAGAIN, and
// the rest). The first 34 agree; the BSD range diverges. The table is
// runtime·cosmo_errno_xlat_tab in sys_cosmo_errno.s, shared with arm64's
// cosmo_xlat_errno_r0 - same 112 bytes, one copy.
//
// internal/runtime/syscall/cosmo reaches this from its own assembly, which
// is what the linkname push in os_cosmo_amd64.go is for.
TEXT runtime·cosmo_xlat_errno_ax(SB),NOSPLIT|NOFRAME,$0
	CMPQ	AX, $107
	JAE	errno_xlat_done
	MOVQ	$runtime·cosmo_errno_xlat_tab(SB), R11
	MOVBLZX	(R11)(AX*1), AX
errno_xlat_done:
	RET
