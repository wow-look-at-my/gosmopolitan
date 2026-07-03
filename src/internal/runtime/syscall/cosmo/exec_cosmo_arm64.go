// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// Darwin (macOS ARM64) process syscall emulation: the pieces os/exec
// needs beyond fork (which syscall.rawVforkSyscall already routes to the
// Syslib in assembly). Linux syscall numbers and layouts in, Apple libc
// functions (dlsym-resolved at startup) out.
//
// Fork-child safety: dup3, setsid, setpgid, execve (plus fcntl, chdir,
// close and write from the base emulation) run in the child between
// fork and exec, where only async-signal-safe functions may be called
// and the stack must not grow. All of them are thin syscall wrappers in
// libSystem and on POSIX's async-signal-safe list; their pointers were
// resolved at osinit, long before the first fork (dlsym itself would
// NOT be fork-child safe); and every function here is nosplit, which
// the linker's nosplit check enforces. The Syslib's fork runs Apple's
// real libc fork including its atfork handlers, so libSystem itself is
// reinitialized and usable in the child.
//
// wait4/pipe2 run only in the parent but are nosplit anyway - they are
// reached inside the _Gsyscall window like the rest of the emulation.
//
// waitid (os.blockUntilWaitable) is intentionally NOT emulated: its
// caller falls back cleanly on ENOSYS to a blocking wait4, which is
// upstream darwin's behavior too (it has no usable waitid and builds
// wait_unimp.go).
//
// Wait status encoding: Apple and Linux agree on the layout (exit code
// in bits 8..15, termination signal in bits 0..6, core-dump flag 0x80),
// but the embedded SIGNAL NUMBERS are the host's (Apple SIGUSR1 is 30,
// Linux's is 10); darwinWait4 rewrites them to Linux numbering at the
// emulation boundary so syscall.WaitStatus decodes correctly.

// Linux arm64 process syscall numbers handled by the slow path.
const (
	sysDUP3    = 24
	sysPIPE2   = 59
	sysKILL    = 129
	sysSETPGID = 154
	sysSETSID  = 157
	sysEXECVE  = 221
	sysWAIT4   = 260
)

// Linux open-flag bits shared by pipe2/dup3 (and, with the same values,
// the socket SOCK_* flags).
const (
	linuxO_CLOEXEC = 0x80000
)

// Wait options. WNOHANG (1) and WUNTRACED (2) coincide; WCONTINUED is 8
// on Linux, 0x10 on Apple.
const (
	linuxWNOHANG    = 1
	linuxWUNTRACED  = 2
	linuxWCONTINUED = 8
	appleWCONTINUED = 0x10
)

// darwinApplyFdFlags applies Linux O_CLOEXEC/O_NONBLOCK to a descriptor
// with fcntl. (The socket SOCK_CLOEXEC/SOCK_NONBLOCK flags have the same
// bit values, so the socket layer reuses this.) It talks to Apple fcntl
// directly - commands and values here are already Apple's - and calls
// the libc trampoline without the darwinCall helper, whose extra frame
// would push its deepest callers (socketpair with flags) over the
// nosplit limit.
//
//go:nosplit
func darwinApplyFdFlags(fd, flags uintptr) uintptr {
	if darwinFns.Fcntl == 0 {
		return darwinENOSYS
	}
	if flags&linuxO_CLOEXEC != 0 {
		if int64(darwinLibcCall6(darwinFns.Fcntl, fd, fcntlF_SETFD, fdCLOEXEC, 0, 0, 0)) == -1 {
			return darwinErrno()
		}
	}
	if flags&linuxO_NONBLOCK != 0 {
		fl := darwinLibcCall6(darwinFns.Fcntl, fd, fcntlF_GETFL, 0, 0, 0, 0)
		if int64(fl) == -1 {
			return darwinErrno()
		}
		if int64(darwinLibcCall6(darwinFns.Fcntl, fd, fcntlF_SETFL, fl|appleO_NONBLOCK, 0, 0, 0)) == -1 {
			return darwinErrno()
		}
	}
	return 0
}

// darwinPipe2 emulates pipe2(2) with pipe + fcntl. Not atomic like the
// Linux syscall, but the flag application cannot fail halfway into a
// visible state: on any error both descriptors are closed again.
//
//go:nosplit
func darwinPipe2(fdsp, flags uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Pipe == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if flags&^uintptr(linuxO_CLOEXEC|linuxO_NONBLOCK) != 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	if _, _, e := darwinCall(darwinFns.Pipe, fdsp, 0, 0, 0, 0, 0); e != 0 {
		return ^uintptr(0), 0, e
	}
	fds := (*[2]int32)(unsafe.Pointer(fdsp))
	for _, fd := range fds {
		if e := darwinApplyFdFlags(uintptr(fd), flags); e != 0 {
			darwinCloseFd(uintptr(fds[0]))
			darwinCloseFd(uintptr(fds[1]))
			return ^uintptr(0), 0, e
		}
	}
	return 0, 0, 0
}

// darwinDup3 emulates dup3(2) with dup2 + fcntl. The FD_CLOEXEC set is
// not atomic with the dup; the only user between fork and exec is
// single-threaded there, and Go userspace otherwise uses F_DUPFD_CLOEXEC.
//
//go:nosplit
func darwinDup3(oldfd, newfd, flags uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Dup2 == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if oldfd == newfd {
		// Linux dup3 rejects this (unlike dup2).
		return ^uintptr(0), 0, darwinEINVAL
	}
	if flags&^uintptr(linuxO_CLOEXEC) != 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	fd, _, e := darwinCall(darwinFns.Dup2, oldfd, newfd, 0, 0, 0, 0)
	if e != 0 {
		return ^uintptr(0), 0, e
	}
	if flags&linuxO_CLOEXEC != 0 {
		if _, _, e := darwinCall(darwinFns.Fcntl, fd, fcntlF_SETFD, fdCLOEXEC, 0, 0, 0); e != 0 {
			return ^uintptr(0), 0, e
		}
	}
	return fd, 0, 0
}

// darwinXlatSignal (sig_cosmo.go) translates the Linux signal number
// for delivery. SIGKILL/SIGSTOP act entirely in the kernel, so
// os.Process.Kill genuinely terminates the target regardless of the
// target's own signal handling.
//
//go:nosplit
func darwinKill(pid, sig uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Kill == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	asig, sigOK := darwinXlatSignal(sig)
	if !sigOK {
		return ^uintptr(0), 0, darwinEINVAL
	}
	return darwinCall(darwinFns.Kill, pid, asig, 0, 0, 0, 0)
}

// darwinWait4 emulates wait4(2): option flags are translated, and the
// signal numbers embedded in the wait status are rewritten from Apple
// to Linux numbering (the status ENCODING - exit code in bits 8..15,
// termination signal in bits 0..6, core flag 0x80, stop marker 0x7f,
// stop signal in bits 8..15 - is identical on both systems, so only
// the signal fields change). syscall.WaitStatus then decodes with the
// Linux numbers the rest of the process uses (a child killed by Apple
// SIGUSR1=30 reports syscall.SIGUSR1=10).
//
// The rusage buffer is passed straight to Apple wait4 and fixed up IN
// PLACE afterwards: struct rusage is 144 bytes on both systems with
// every field at the same offset EXCEPT tv_usec in the two timevals,
// which Apple declares as int32-plus-padding where Linux has int64.
// Widening those two fields in place (the padding bytes Apple never
// wrote become the upper halves) avoids a 144-byte local that would
// blow the nosplit budget of this chain.
//
//go:nosplit
func darwinWait4(pid, wstatus, options, rusage uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Wait4 == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if options&^uintptr(linuxWNOHANG|linuxWUNTRACED|linuxWCONTINUED) != 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	aopt := options & (linuxWNOHANG | linuxWUNTRACED)
	if options&linuxWCONTINUED != 0 {
		aopt |= appleWCONTINUED
	}
	r1, r2, errno = darwinCall(darwinFns.Wait4, pid, wstatus, aopt, rusage, 0, 0)
	if errno == 0 && rusage != 0 {
		// ru_utime.tv_usec at offset 8, ru_stime.tv_usec at offset 24.
		u := int64(*(*int32)(unsafe.Pointer(rusage + 8)))
		*(*int64)(unsafe.Pointer(rusage + 8)) = u
		s := int64(*(*int32)(unsafe.Pointer(rusage + 24)))
		*(*int64)(unsafe.Pointer(rusage + 24)) = s
	}
	if errno == 0 && wstatus != 0 && int64(r1) > 0 {
		sp := (*uint32)(unsafe.Pointer(wstatus))
		*sp = darwinXlatWaitStatus(*sp)
	}
	return r1, r2, errno
}
