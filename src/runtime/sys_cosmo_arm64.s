// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

//
// System calls for arm64, Cosmopolitan
// Supports both Linux (direct SVC) and macOS (via Syslib from APE loader)
//

#include "go_asm.h"
#include "go_tls.h"
#include "textflag.h"
#include "cgo/abi_arm64.h"

#define AT_FDCWD -100

#define CLOCK_REALTIME 0
#define CLOCK_MONOTONIC 1

// Host OS indicators (must match os_cosmo_arm64.go)
#define HOSTXNU 8

// Linux ARM64 syscall numbers
#define SYS_exit		93
#define SYS_read		63
#define SYS_write		64
#define SYS_openat		56
#define SYS_close		57
#define SYS_pipe2		59
#define SYS_nanosleep		101
#define SYS_mmap		222
#define SYS_munmap		215
#define SYS_setitimer		103
#define SYS_clone		220
#define SYS_sched_yield		124
#define SYS_rt_sigreturn	139
#define SYS_rt_sigaction	134
#define SYS_rt_sigprocmask	135
#define SYS_sigaltstack		132
#define SYS_madvise		233
#define SYS_mincore		232
#define SYS_getpid		172
#define SYS_gettid		178
#define SYS_kill		129
#define SYS_tgkill		131
#define SYS_futex		98
#define SYS_sched_getaffinity	123
#define SYS_exit_group		94
#define SYS_clock_gettime	113
#define SYS_faccessat		48
#define SYS_socket		198
#define SYS_connect		203
#define SYS_brk			214
#define SYS_timer_create	107
#define SYS_timer_settime	110
#define SYS_timer_delete	111

// Syslib structure offsets (must match os_cosmo_arm64.go and ape-m1.c)
// Using inline constants since Go asm doesn't support arbitrary #define macros.
// Each pointer is 8 bytes on ARM64
//
// Offset reference:
// 0    magic (int32)
// 4    version (int32)
// 8    fork
// 16   pipe
// 24   clock_gettime
// 32   nanosleep
// 40   mmap
// 48   pthread_jit_write_protect_supported_np
// 56   pthread_jit_write_protect_np
// 64   sys_icache_invalidate
// 72   pthread_create
// 80   pthread_exit
// 88   pthread_kill
// 96   pthread_sigmask
// 104  pthread_setname_np
// 112  dispatch_semaphore_create
// 120  dispatch_semaphore_signal
// 128  dispatch_semaphore_wait
// 136  dispatch_walltime
// 144  pthread_self
// 152  dispatch_release
// 160  raise
// 168  pthread_join
// 176  pthread_yield_np
// 184  pthread_stack_min (int32)
// 188  sizeof_pthread_attr_t (int32)
// 192  pthread_attr_init
// 200  pthread_attr_destroy
// 208  pthread_attr_setstacksize
// 216  pthread_attr_setguardsize
// 224  exit
// 232  close
// 240  munmap
// 248  openat
// 256  write
// 264  read
// 272  sigaction
// 280  pselect
// 288  mprotect
// 296  sigaltstack

// libcCall calls a C function through the Syslib.
// fn is the function pointer, arg is a pointer to the argument structure.
// This follows the Apple ARM64 calling convention.
TEXT runtime·libcCall(SB),NOSPLIT,$0-24
	MOVD	fn+0(FP), R12		// function pointer
	MOVD	arg+8(FP), R0		// argument (struct pointer or first arg)
	SUB	$16, RSP		// align stack for C ABI
	BL	(R12)
	ADD	$16, RSP
	MOVD	R0, ret+16(FP)
	RET

// dispatch_semaphore_create(value int64) dispatch_semaphore_t
// Syslib offset 112
TEXT runtime·dispatch_semaphore_create_trampoline(SB),NOSPLIT,$0-16
	MOVD	runtime·__syslib(SB), R9
	CBZ	R9, dsema_create_fail
	MOVD	112(R9), R12
	CBZ	R12, dsema_create_fail
	MOVD	value+0(FP), R0
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	MOVD	R0, ret+8(FP)
	RET
dsema_create_fail:
	MOVD	$0, ret+8(FP)
	RET

// dispatch_semaphore_signal(sema uintptr) int64
// Syslib offset 120
TEXT runtime·dispatch_semaphore_signal_trampoline(SB),NOSPLIT,$0-16
	MOVD	runtime·__syslib(SB), R9
	CBZ	R9, dsema_signal_fail
	MOVD	120(R9), R12
	CBZ	R12, dsema_signal_fail
	MOVD	sema+0(FP), R0
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	MOVD	R0, ret+8(FP)
	RET
dsema_signal_fail:
	MOVD	$0, ret+8(FP)
	RET

// dispatch_semaphore_wait(sema uintptr, timeout uint64) int64
// Syslib offset 128
TEXT runtime·dispatch_semaphore_wait_trampoline(SB),NOSPLIT,$0-24
	MOVD	runtime·__syslib(SB), R9
	CBZ	R9, dsema_wait_fail
	MOVD	128(R9), R12
	CBZ	R12, dsema_wait_fail
	MOVD	sema+0(FP), R0
	MOVD	timeout+8(FP), R1
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	MOVD	R0, ret+16(FP)
	RET
dsema_wait_fail:
	MOVD	$-1, R0
	MOVD	R0, ret+16(FP)
	RET

// Helper macro: check if we're on macOS and jump to label if so
// Clobbers R9
#define CHECK_DARWIN(label) \
	MOVW	runtime·__hostos(SB), R9; \
	CMPW	$HOSTXNU, R9; \
	BEQ	label

TEXT runtime·exit(SB),NOSPLIT|NOFRAME,$0-4
	CHECK_DARWIN(exit_darwin)
	// Linux path: direct syscall
	MOVW	code+0(FP), R0
	MOVD	$SYS_exit_group, R8
	SVC
	RET
exit_darwin:
	// macOS path: call Syslib exit function
	MOVD	runtime·__syslib(SB), R9
	MOVD	224(R9), R12
	MOVW	code+0(FP), R0
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	RET

// func exitThread(wait *atomic.Uint32)
TEXT runtime·exitThread(SB),NOSPLIT|NOFRAME,$0-8
	MOVD	wait+0(FP), R0
	// We're done using the stack.
	MOVW	$0, R1
	STLRW	R1, (R0)
	CHECK_DARWIN(exitThread_darwin)
	// Linux path
	MOVW	$0, R0	// exit code
	MOVD	$SYS_exit, R8
	SVC
	JMP	0(PC)
exitThread_darwin:
	// macOS path: call pthread_exit
	MOVD	runtime·__syslib(SB), R9
	MOVD	80(R9), R12
	MOVD	$0, R0
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	JMP	0(PC)

TEXT runtime·open(SB),NOSPLIT,$0-20
	CHECK_DARWIN(open_darwin)
	// Linux path
	MOVD	$AT_FDCWD, R0
	MOVD	name+0(FP), R1
	MOVW	mode+8(FP), R2
	MOVW	perm+12(FP), R3
	MOVD	$SYS_openat, R8
	SVC
	CMN	$4095, R0
	BCC	open_done
	MOVW	$-1, R0
open_done:
	MOVW	R0, ret+16(FP)
	RET
open_darwin:
	// macOS path: call Syslib openat
	MOVD	runtime·__syslib(SB), R9
	MOVD	248(R9), R12
	MOVD	$AT_FDCWD, R0
	MOVD	name+0(FP), R1
	MOVW	mode+8(FP), R2
	MOVW	perm+12(FP), R3
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	CMN	$4095, R0
	BCC	open_darwin_done
	MOVW	$-1, R0
open_darwin_done:
	MOVW	R0, ret+16(FP)
	RET

TEXT runtime·closefd(SB),NOSPLIT,$0-12
	CHECK_DARWIN(closefd_darwin)
	// Linux path
	MOVW	fd+0(FP), R0
	MOVD	$SYS_close, R8
	SVC
	CMN	$4095, R0
	BCC	closefd_done
	MOVW	$-1, R0
closefd_done:
	MOVW	R0, ret+8(FP)
	RET
closefd_darwin:
	MOVD	runtime·__syslib(SB), R9
	MOVD	232(R9), R12
	MOVW	fd+0(FP), R0
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	CMN	$4095, R0
	BCC	closefd_darwin_done
	MOVW	$-1, R0
closefd_darwin_done:
	MOVW	R0, ret+8(FP)
	RET

TEXT runtime·write1(SB),NOSPLIT,$0-28
	CHECK_DARWIN(write1_darwin)
	// Linux path
	MOVD	fd+0(FP), R0
	MOVD	p+8(FP), R1
	MOVW	n+16(FP), R2
	MOVD	$SYS_write, R8
	SVC
	MOVW	R0, ret+24(FP)
	RET
write1_darwin:
	MOVD	runtime·__syslib(SB), R9
	MOVD	256(R9), R12
	MOVD	fd+0(FP), R0
	MOVD	p+8(FP), R1
	MOVW	n+16(FP), R2
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	MOVW	R0, ret+24(FP)
	RET

TEXT runtime·read(SB),NOSPLIT,$0-28
	CHECK_DARWIN(read_darwin)
	// Linux path
	MOVW	fd+0(FP), R0
	MOVD	p+8(FP), R1
	MOVW	n+16(FP), R2
	MOVD	$SYS_read, R8
	SVC
	MOVW	R0, ret+24(FP)
	RET
read_darwin:
	MOVD	runtime·__syslib(SB), R9
	MOVD	264(R9), R12
	MOVW	fd+0(FP), R0
	MOVD	p+8(FP), R1
	MOVW	n+16(FP), R2
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	MOVW	R0, ret+24(FP)
	RET

// func pipe2(flags int32) (r, w int32, errno int32)
TEXT runtime·pipe2(SB),NOSPLIT,$16-20
	CHECK_DARWIN(pipe2_darwin)
	// Linux path
	MOVD	$r+8(FP), R0
	MOVW	flags+0(FP), R1
	MOVW	$SYS_pipe2, R8
	SVC
	MOVW	R0, errno+16(FP)
	RET
pipe2_darwin:
	// macOS pipe() doesn't support flags, but Syslib provides pipe()
	// For now, ignore flags and use basic pipe
	MOVD	runtime·__syslib(SB), R9
	MOVD	16(R9), R12
	MOVD	$r+8(FP), R0	// pointer to int[2] for r,w
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	MOVW	R0, errno+16(FP)
	RET

TEXT runtime·usleep(SB),NOSPLIT,$24-4
	MOVWU	usec+0(FP), R3
	MOVD	R3, R5
	MOVW	$1000000, R4
	UDIV	R4, R3
	MOVD	R3, 8(RSP)	// seconds
	MUL	R3, R4
	SUB	R4, R5
	MOVW	$1000, R4
	MUL	R4, R5
	MOVD	R5, 16(RSP)	// nanoseconds

	CHECK_DARWIN(usleep_darwin)
	// Linux path: nanosleep(&ts, 0)
	ADD	$8, RSP, R0
	MOVD	$0, R1
	MOVD	$SYS_nanosleep, R8
	SVC
	RET
usleep_darwin:
	// macOS path
	MOVD	runtime·__syslib(SB), R9
	MOVD	32(R9), R12
	ADD	$8, RSP, R0	// timespec pointer
	MOVD	$0, R1		// remainder (NULL)
	BL	(R12)
	RET

TEXT runtime·gettid(SB),NOSPLIT,$0-4
	CHECK_DARWIN(gettid_darwin)
	MOVD	$SYS_gettid, R8
	SVC
	MOVW	R0, ret+0(FP)
	RET
gettid_darwin:
	// macOS: use pthread_self as a thread identifier
	MOVD	runtime·__syslib(SB), R9
	MOVD	144(R9), R12
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	MOVW	R0, ret+0(FP)
	RET

TEXT runtime·raise(SB),NOSPLIT,$0
	CHECK_DARWIN(raise_darwin)
	// Linux path
	MOVD	$SYS_getpid, R8
	SVC
	MOVW	R0, R19
	MOVD	$SYS_gettid, R8
	SVC
	MOVW	R0, R1	// arg 2 tid
	MOVW	R19, R0	// arg 1 pid
	MOVW	sig+0(FP), R2	// arg 3
	MOVD	$SYS_tgkill, R8
	SVC
	RET
raise_darwin:
	MOVD	runtime·__syslib(SB), R9
	MOVD	160(R9), R12
	MOVW	sig+0(FP), R0
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	RET

TEXT runtime·raiseproc(SB),NOSPLIT,$0
	CHECK_DARWIN(raiseproc_darwin)
	// Linux path
	MOVD	$SYS_getpid, R8
	SVC
	MOVW	R0, R0		// arg 1 pid
	MOVW	sig+0(FP), R1	// arg 2
	MOVD	$SYS_kill, R8
	SVC
	RET
raiseproc_darwin:
	// Use raise() which sends signal to current process
	MOVD	runtime·__syslib(SB), R9
	MOVD	160(R9), R12
	MOVW	sig+0(FP), R0
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	RET

TEXT ·getpid(SB),NOSPLIT,$0-8
	CHECK_DARWIN(getpid_darwin)
	// Linux path
	MOVD	$SYS_getpid, R8
	SVC
	MOVD	R0, ret+0(FP)
	RET
getpid_darwin:
	// macOS: use getpid from syslib (offset 112)
	MOVD	runtime·__syslib(SB), R9
	MOVD	112(R9), R12
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	MOVD	R0, ret+0(FP)
	RET

TEXT ·tgkill(SB),NOSPLIT,$0-24
	CHECK_DARWIN(tgkill_darwin)
	MOVD	tgid+0(FP), R0
	MOVD	tid+8(FP), R1
	MOVD	sig+16(FP), R2
	MOVD	$SYS_tgkill, R8
	SVC
	RET
tgkill_darwin:
	// macOS: use pthread_kill
	MOVD	runtime·__syslib(SB), R9
	MOVD	88(R9), R12
	MOVD	tid+8(FP), R0	// pthread_t
	MOVD	sig+16(FP), R1	// signal
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	RET

TEXT runtime·setitimer(SB),NOSPLIT,$0-24
	CHECK_DARWIN(setitimer_darwin)
	// Linux path
	MOVW	mode+0(FP), R0
	MOVD	new+8(FP), R1
	MOVD	old+16(FP), R2
	MOVD	$SYS_setitimer, R8
	SVC
	RET
setitimer_darwin:
	// macOS: setitimer not in Syslib, would need to add
	// For now, this is a stub - profiling timers won't work
	RET

TEXT runtime·mincore(SB),NOSPLIT,$0-28
	CHECK_DARWIN(mincore_darwin)
	// Linux path
	MOVD	addr+0(FP), R0
	MOVD	n+8(FP), R1
	MOVD	dst+16(FP), R2
	MOVD	$SYS_mincore, R8
	SVC
	MOVW	R0, ret+24(FP)
	RET
mincore_darwin:
	// macOS ARM64: mincore not in Syslib, return -1 (ENOSYS)
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// func walltime() (sec int64, nsec int32)
TEXT runtime·walltime(SB),NOSPLIT,$24-12
	MOVD	RSP, R20	// R20 is unchanged by C code
	MOVD	RSP, R1

	MOVD	g_m(g), R21	// R21 = m

	// Set vdsoPC and vdsoSP for SIGPROF traceback.
	MOVD	m_vdsoPC(R21), R2
	MOVD	m_vdsoSP(R21), R3
	MOVD	R2, 8(RSP)
	MOVD	R3, 16(RSP)

	MOVD	$ret-8(FP), R2 // caller's SP
	MOVD	LR, m_vdsoPC(R21)
	MOVD	R2, m_vdsoSP(R21)

	MOVD	m_curg(R21), R0
	CMP	g, R0
	BNE	walltime_noswitch

	MOVD	m_g0(R21), R3
	MOVD	(g_sched+gobuf_sp)(R3), R1	// Set RSP to g0 stack

walltime_noswitch:
	SUB	$16, R1
	BIC	$15, R1	// Align for C code
	MOVD	R1, RSP

	CHECK_DARWIN(walltime_darwin)
	// Linux path: Use syscall directly
	MOVW	$CLOCK_REALTIME, R0
	MOVD	RSP, R1
	MOVD	$SYS_clock_gettime, R8
	SVC
	B	walltime_finish

walltime_darwin:
	// macOS path: call Syslib clock_gettime
	MOVD	runtime·__syslib(SB), R9
	MOVD	24(R9), R12
	MOVW	$CLOCK_REALTIME, R0
	MOVD	RSP, R1		// timespec pointer
	BL	(R12)

walltime_finish:
	MOVD	0(RSP), R3	// sec
	MOVD	8(RSP), R5	// nsec

	MOVD	R20, RSP	// restore SP
	// Restore vdsoPC, vdsoSP
	MOVD	16(RSP), R1
	MOVD	R1, m_vdsoSP(R21)
	MOVD	8(RSP), R1
	MOVD	R1, m_vdsoPC(R21)

	MOVD	R3, sec+0(FP)
	MOVW	R5, nsec+8(FP)
	RET

TEXT runtime·nanotime1(SB),NOSPLIT,$24-8
	MOVD	RSP, R20	// R20 is unchanged by C code
	MOVD	RSP, R1

	MOVD	g_m(g), R21	// R21 = m

	// Set vdsoPC and vdsoSP for SIGPROF traceback.
	MOVD	m_vdsoPC(R21), R2
	MOVD	m_vdsoSP(R21), R3
	MOVD	R2, 8(RSP)
	MOVD	R3, 16(RSP)

	MOVD	$ret-8(FP), R2 // caller's SP
	MOVD	LR, m_vdsoPC(R21)
	MOVD	R2, m_vdsoSP(R21)

	MOVD	m_curg(R21), R0
	CMP	g, R0
	BNE	nanotime_noswitch

	MOVD	m_g0(R21), R3
	MOVD	(g_sched+gobuf_sp)(R3), R1	// Set RSP to g0 stack

nanotime_noswitch:
	SUB	$16, R1
	BIC	$15, R1
	MOVD	R1, RSP

	CHECK_DARWIN(nanotime_darwin)
	// Linux path: Use syscall directly
	MOVW	$CLOCK_MONOTONIC, R0
	MOVD	RSP, R1
	MOVD	$SYS_clock_gettime, R8
	SVC
	B	nanotime_finish

nanotime_darwin:
	// macOS path: call Syslib clock_gettime
	MOVD	runtime·__syslib(SB), R9
	MOVD	24(R9), R12
	MOVW	$CLOCK_MONOTONIC, R0
	MOVD	RSP, R1		// timespec pointer
	BL	(R12)

nanotime_finish:
	MOVD	0(RSP), R3	// sec
	MOVD	8(RSP), R5	// nsec

	MOVD	R20, RSP	// restore SP
	MOVD	16(RSP), R1
	MOVD	R1, m_vdsoSP(R21)
	MOVD	8(RSP), R1
	MOVD	R1, m_vdsoPC(R21)

	// sec is in R3, nsec in R5
	// return nsec in R3
	MOVD	$1000000000, R4
	MUL	R4, R3
	ADD	R5, R3
	// DEBUG: exit 68 - nanotime1 return
	// MOVD	$68, R0
	// MOVD	runtime·__syslib(SB), R9
	// MOVD	224(R9), R12
	// BL	(R12)
	MOVD	R3, ret+0(FP)
	RET

TEXT runtime·rtsigprocmask(SB),NOSPLIT,$0-28
	CHECK_DARWIN(rtsigprocmask_darwin)
	// Linux path
	MOVW	how+0(FP), R0
	MOVD	new+8(FP), R1
	MOVD	old+16(FP), R2
	MOVW	size+24(FP), R3
	MOVD	$SYS_rt_sigprocmask, R8
	SVC
	CMN	$4095, R0
	BCC	rtsigprocmask_done
	MOVD	$0, R0
	MOVD	R0, (R0)	// crash
rtsigprocmask_done:
	RET
rtsigprocmask_darwin:
	// macOS: use pthread_sigmask
	MOVD	runtime·__syslib(SB), R9
	MOVD	96(R9), R12
	MOVW	how+0(FP), R0
	MOVD	new+8(FP), R1
	MOVD	old+16(FP), R2
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	RET

TEXT runtime·rt_sigaction(SB),NOSPLIT,$0-36
	CHECK_DARWIN(rt_sigaction_darwin)
	// Linux path
	MOVD	sig+0(FP), R0
	MOVD	new+8(FP), R1
	MOVD	old+16(FP), R2
	MOVD	size+24(FP), R3
	MOVD	$SYS_rt_sigaction, R8
	SVC
	MOVW	R0, ret+32(FP)
	RET
rt_sigaction_darwin:
	// macOS: sigaction structure is different from Linux
	// For now, return success without calling sigaction
	// This means signal handling won't work properly, but allows basic execution
	// TODO: Implement proper sigaction translation layer
	MOVW	$0, ret+32(FP)
	RET

TEXT runtime·sigfwd(SB),NOSPLIT,$0-32
	MOVW	sig+8(FP), R0
	MOVD	info+16(FP), R1
	MOVD	ctx+24(FP), R2
	MOVD	fn+0(FP), R11
	BL	(R11)
	RET

// Called from c-abi, R0: sig, R1: info, R2: cxt
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME,$176
	// Save callee-save registers in the case of signal forwarding.
	SAVE_R19_TO_R28(8*4)
	SAVE_F8_TO_F15(8*14)

	// this might be called in external code context,
	// where g is not set.
	// first save R0, because runtime·load_g will clobber it
	MOVW	R0, 8(RSP)
	MOVBU	runtime·iscgo(SB), R0
	CBZ	R0, 2(PC)
	BL	runtime·load_g(SB)

	// Restore signum to R0.
	MOVW	8(RSP), R0
	// R1 and R2 already contain info and ctx, respectively.
	MOVD	$runtime·sigtrampgo<ABIInternal>(SB), R3
	BL	(R3)

	// Restore callee-save registers.
	RESTORE_R19_TO_R28(8*4)
	RESTORE_F8_TO_F15(8*14)

	RET

TEXT runtime·cgoSigtramp(SB),NOSPLIT|NOFRAME,$0
	B	runtime·sigtramp(SB)

TEXT runtime·mmap(SB),NOSPLIT,$0
	CHECK_DARWIN(mmap_darwin)
	// Linux path
	MOVD	addr+0(FP), R0
	MOVD	n+8(FP), R1
	MOVW	prot+16(FP), R2
	MOVW	flags+20(FP), R3
	MOVW	fd+24(FP), R4
	MOVW	off+28(FP), R5

	MOVD	$SYS_mmap, R8
	SVC
	CMN	$4095, R0
	BCC	mmap_ok
	NEG	R0,R0
	MOVD	$0, p+32(FP)
	MOVD	R0, err+40(FP)
	RET
mmap_ok:
	MOVD	R0, p+32(FP)
	MOVD	$0, err+40(FP)
	RET
mmap_darwin:
	// macOS path: call Syslib mmap
	// Save LR
	MOVD	LR, R19
	MOVD	runtime·__syslib(SB), R9
	CMP	$0, R9
	BEQ	mmap_darwin_nosyslib
	MOVD	40(R9), R12              // mmap function pointer
	CMP	$0, R12
	BEQ	mmap_darwin_nommap

	// Load all arguments BEFORE any stack manipulation
	// R0-R5 will hold the mmap arguments
	MOVD	addr+0(FP), R0
	MOVD	n+8(FP), R1
	MOVW	prot+16(FP), R2
	MOVW	flags+20(FP), R3
	MOVW	fd+24(FP), R4
	MOVWU	off+28(FP), R5

	// Translate Linux flags to macOS flags:
	// Linux MAP_ANONYMOUS = 0x20, macOS MAP_ANON = 0x1000
	AND	$0x20, R3, R6            // Extract MAP_ANONYMOUS bit
	CMP	$0, R6
	BEQ	mmap_darwin_no_anon
	AND	$~0x20, R3, R3           // Clear Linux MAP_ANONYMOUS
	ORR	$0x1000, R3, R3          // Set macOS MAP_ANON
mmap_darwin_no_anon:

	// Sign-extend fd for -1 to become proper 64-bit -1
	SXTW	R4, R4

	// Now save R0-R5 and R12 to stack, then align stack
	MOVD	RSP, R7                  // R7 = original SP
	SUB	$80, RSP                 // Allocate space for saved values + alignment
	MOVD	RSP, R6
	AND	$~15, R6, R6
	MOVD	R6, RSP                  // RSP now aligned

	// Save original SP, mmap pointer, and all arguments
	MOVD	R7, (RSP)                // [RSP+0] = original SP
	MOVD	R12, 8(RSP)              // [RSP+8] = mmap function pointer
	MOVD	R0, 16(RSP)              // [RSP+16] = addr
	MOVD	R1, 24(RSP)              // [RSP+24] = n
	MOVD	R2, 32(RSP)              // [RSP+32] = prot
	MOVD	R3, 40(RSP)              // [RSP+40] = flags (translated)
	MOVD	R4, 48(RSP)              // [RSP+48] = fd
	MOVD	R5, 56(RSP)              // [RSP+56] = off

	// Reload arguments from stack and call mmap
	MOVD	16(RSP), R0
	MOVD	24(RSP), R1
	MOVD	32(RSP), R2
	MOVD	40(RSP), R3
	MOVD	48(RSP), R4
	MOVD	56(RSP), R5
	MOVD	8(RSP), R12

	// Load all arguments from stack for the mmap call
	MOVD	16(RSP), R0              // addr from stack
	MOVD	24(RSP), R1              // n from stack
	MOVD	32(RSP), R2              // prot from stack
	MOVD	40(RSP), R3              // flags from stack (translated)
	MOVD	48(RSP), R4              // fd from stack
	MOVD	56(RSP), R5              // off from stack

	BL	(R12)                        // Call mmap

	// R0 now contains mmap result
	// Save result and restore stack
	MOVD	R0, R6                   // Save result in R6
	MOVD	(RSP), R7                // Original SP
	MOVD	R7, RSP                  // Restore SP
	MOVD	R19, LR                  // Restore LR

	// Check for error: if R6 >= -4095 (unsigned), it's an error (-errno)
	CMN	$4095, R6
	BCC	mmap_darwin_ok

	// Error case - return error
	NEG	R6, R6
	MOVD	$0, p+32(FP)
	MOVD	R6, err+40(FP)
	RET

mmap_darwin_ok:
	MOVD	R6, p+32(FP)
	MOVD	$0, err+40(FP)
	RET
mmap_darwin_nosyslib:
	MOVD	$0, p+32(FP)
	MOVD	$12, R0
	MOVD	R0, err+40(FP)
	RET
mmap_darwin_nommap:
	MOVD	$0, p+32(FP)
	MOVD	$12, R0
	MOVD	R0, err+40(FP)
	RET

TEXT runtime·munmap(SB),NOSPLIT,$0
	CHECK_DARWIN(munmap_darwin)
	// Linux path
	MOVD	addr+0(FP), R0
	MOVD	n+8(FP), R1
	MOVD	$SYS_munmap, R8
	SVC
	CMN	$4095, R0
	BCC	munmap_cool
	MOVD	R0, 0xf0(R0)
munmap_cool:
	RET
munmap_darwin:
	MOVD	runtime·__syslib(SB), R9
	MOVD	240(R9), R12
	MOVD	addr+0(FP), R0
	MOVD	n+8(FP), R1
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	CMN	$4095, R0
	BCC	munmap_darwin_cool
	MOVD	R0, 0xf0(R0)
munmap_darwin_cool:
	RET

TEXT runtime·madvise(SB),NOSPLIT,$0-28
	CHECK_DARWIN(madvise_darwin)
	// Linux path
	MOVD	addr+0(FP), R0
	MOVD	n+8(FP), R1
	MOVW	flags+16(FP), R2
	MOVD	$SYS_madvise, R8
	SVC
	MOVW	R0, ret+24(FP)
	RET
madvise_darwin:
	// macOS ARM64: madvise not in Syslib, return 0 (success)
	// The Go runtime handles madvise failures gracefully
	MOVW	$0, ret+24(FP)
	RET

// int64 futex(int32 *uaddr, int32 op, int32 val,
//	struct timespec *timeout, int32 *uaddr2, int32 val2);
TEXT runtime·futex(SB),NOSPLIT,$0
	CHECK_DARWIN(futex_darwin)
	// Linux path
	MOVD	addr+0(FP), R0
	MOVW	op+8(FP), R1
	MOVW	val+12(FP), R2
	MOVD	ts+16(FP), R3
	MOVD	addr2+24(FP), R4
	MOVW	val3+32(FP), R5
	MOVD	$SYS_futex, R8
	SVC
	MOVW	R0, ret+40(FP)
	RET
futex_darwin:
	// macOS: futex not available, use dispatch_semaphore
	// This is a simplified implementation for basic cases
	// For now, return ENOSYS to indicate not supported
	// The Go runtime will need alternative synchronization
	MOVW	$-38, R0	// ENOSYS
	MOVW	R0, ret+40(FP)
	RET

// int64 clone(int32 flags, void *stk, M *mp, G *gp, void (*fn)(void));
TEXT runtime·clone(SB),NOSPLIT,$0
	CHECK_DARWIN(clone_darwin)
	// Linux path
	MOVW	flags+0(FP), R0
	MOVD	stk+8(FP), R1

	// Copy mp, gp, fn off parent stack for use by child.
	MOVD	mp+16(FP), R10
	MOVD	gp+24(FP), R11
	MOVD	fn+32(FP), R12

	MOVD	R10, -8(R1)
	MOVD	R11, -16(R1)
	MOVD	R12, -24(R1)
	MOVD	$1234, R10
	MOVD	R10, -32(R1)

	MOVD	$SYS_clone, R8
	SVC

	// In parent, return.
	CMP	ZR, R0
	BEQ	clone_child
	MOVW	R0, ret+40(FP)
	RET
clone_child:

	// In child, on new stack.
	MOVD	-32(RSP), R10
	MOVD	$1234, R0
	CMP	R0, R10
	BEQ	clone_good
	MOVD	$0, R0
	MOVD	R0, (R0)	// crash

clone_good:
	// Initialize m->procid to Linux tid
	MOVD	$SYS_gettid, R8
	SVC

	MOVD	-24(RSP), R12     // fn
	MOVD	-16(RSP), R11     // g
	MOVD	-8(RSP), R10      // m

	CMP	$0, R10
	BEQ	clone_nog
	CMP	$0, R11
	BEQ	clone_nog

	MOVD	R0, m_procid(R10)

	// In child, set up new stack
	MOVD	R10, g_m(R11)
	MOVD	R11, g

clone_nog:
	// Call fn
	MOVD	R12, R0
	BL	(R0)

	// It shouldn't return. If it does, exit that thread.
	MOVW	$111, R0
clone_again:
	MOVD	$SYS_exit, R8
	SVC
	B	clone_again	// keep exiting

clone_darwin:
	// macOS: use pthread_create instead of clone
	// Get pthread_create from syslib (offset 72)
	MOVD	runtime·__syslib(SB), R9
	CBZ	R9, clone_darwin_fail
	MOVD	72(R9), R12
	CBZ	R12, clone_darwin_fail

	// pthread_create signature:
	// int pthread_create(pthread_t *thread, const pthread_attr_t *attr,
	//                    void *(*start_routine)(void *), void *arg)
	//
	// We need to create a thread that runs mstart with the new m.
	// For pthread, we'll use mstart_stub_cosmo which expects mp in the argument.

	// Clone args: flags+0, stk+8, mp+16, gp+24, fn+32
	// Load mp BEFORE modifying RSP (FP offsets may be affected by RSP changes)
	MOVD	mp+16(FP), R19		// R19 = mp (callee-saved, will survive BL)

	// Allocate space for pthread_t and save callee-saves we'll use
	SUB	$48, RSP
	STP	(R19, R20), 32(RSP)	// Save R19 (mp) and R20 (unused but paired)

	MOVD	RSP, R0			// &thread (first 16 bytes)
	MOVD	$0, R1			// attr = NULL (default)
	MOVD	$runtime·mstart_stub_cosmo(SB), R2	// start_routine
	MOVD	R19, R3			// arg = mp

	BL	(R12)			// pthread_create

	// Restore callee-saves
	LDP	32(RSP), (R19, R20)
	ADD	$48, RSP

	// pthread_create returns 0 on success, error code on failure
	// clone returns child tid on success (> 0), negative errno on failure
	CMP	$0, R0
	BNE	clone_darwin_fail

	// Return a fake positive tid
	MOVW	$1, R0
	MOVW	R0, ret+40(FP)
	RET

clone_darwin_fail:
	MOVW	$-38, R0	// ENOSYS
	MOVW	R0, ret+40(FP)
	RET

// mstart_stub_cosmo is the entry point for threads created with pthread_create
// It receives mp as the argument and sets up the thread to run mstart
// Matches the structure of darwin's mstart_stub
TEXT runtime·mstart_stub_cosmo(SB),NOSPLIT,$160
	// R0 points to the m.
	// We are already on m's g0 stack (provided by pthread).
	// This matches darwin's mstart_stub exactly.

	// Save callee-save registers.
	SAVE_R19_TO_R28(8)
	SAVE_F8_TO_F15(88)

	MOVD	m_g0(R0), g
	BL	runtime·save_g(SB)

	BL	runtime·mstart(SB)

	// Restore callee-save registers.
	RESTORE_R19_TO_R28(8)
	RESTORE_F8_TO_F15(88)

	// Go is all done with this OS thread.
	// Tell pthread everything is ok (we never join with this thread, so
	// the value here doesn't really matter).
	MOVD	$0, R0

	RET

TEXT runtime·sigaltstack(SB),NOSPLIT,$0
	CHECK_DARWIN(sigaltstack_darwin)
	// Linux path
	MOVD	new+0(FP), R0
	MOVD	old+8(FP), R1
	MOVD	$SYS_sigaltstack, R8
	SVC
	CMN	$4095, R0
	BCC	sigaltstack_ok
	MOVD	$0, R0
	MOVD	R0, (R0)	// crash
sigaltstack_ok:
	RET
sigaltstack_darwin:
	// macOS: use Syslib sigaltstack
	MOVD	runtime·__syslib(SB), R9
	CBZ	R9, sigaltstack_darwin_ok  // No syslib, skip
	MOVD	296(R9), R12
	CBZ	R12, sigaltstack_darwin_ok  // Function not available, skip
	MOVD	new+0(FP), R0
	MOVD	old+8(FP), R1
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	// Note: darwin sigaltstack returns 0 on success, -1 on error
	// We treat non-zero as non-fatal here
sigaltstack_darwin_ok:
	RET

TEXT runtime·osyield(SB),NOSPLIT,$0
	CHECK_DARWIN(osyield_darwin)
	// Linux path
	MOVD	$SYS_sched_yield, R8
	SVC
	RET
osyield_darwin:
	// macOS: use pthread_yield_np
	MOVD	runtime·__syslib(SB), R9
	MOVD	176(R9), R12
	SUB	$16, RSP
	BL	(R12)
	ADD	$16, RSP
	RET

TEXT runtime·sched_getaffinity(SB),NOSPLIT,$0-28
	// Linux only - macOS doesn't have this
	CHECK_DARWIN(sched_getaffinity_darwin)
	MOVD	pid+0(FP), R0
	MOVD	len+8(FP), R1
	MOVD	buf+16(FP), R2
	MOVD	$SYS_sched_getaffinity, R8
	SVC
	MOVW	R0, ret+24(FP)
	RET
sched_getaffinity_darwin:
	// Return 1 CPU as default on macOS
	MOVW	$-38, R0	// ENOSYS
	MOVW	R0, ret+24(FP)
	RET

// int access(const char *name, int mode)
TEXT runtime·access(SB),NOSPLIT,$0-20
	// Use faccessat on both platforms
	MOVD	$AT_FDCWD, R0
	MOVD	name+0(FP), R1
	MOVW	mode+8(FP), R2
	MOVD	$SYS_faccessat, R8
	SVC
	MOVW	R0, ret+16(FP)
	RET

// int connect(int fd, const struct sockaddr *addr, socklen_t len)
TEXT runtime·connect(SB),NOSPLIT,$0-28
	MOVW	fd+0(FP), R0
	MOVD	addr+8(FP), R1
	MOVW	len+16(FP), R2
	MOVD	$SYS_connect, R8
	SVC
	MOVW	R0, ret+24(FP)
	RET

// int socket(int domain, int typ, int prot)
TEXT runtime·socket(SB),NOSPLIT,$0-20
	MOVW	domain+0(FP), R0
	MOVW	typ+4(FP), R1
	MOVW	prot+8(FP), R2
	MOVD	$SYS_socket, R8
	SVC
	MOVW	R0, ret+16(FP)
	RET

// func sbrk0() uintptr
TEXT runtime·sbrk0(SB),NOSPLIT,$0-8
	// Implemented as brk(NULL).
	MOVD	$0, R0
	MOVD	$SYS_brk, R8
	SVC
	MOVD	R0, ret+0(FP)
	RET

// ARM64 doesn't use settls - TLS is set differently
TEXT runtime·settls(SB),NOSPLIT,$0
	RET
