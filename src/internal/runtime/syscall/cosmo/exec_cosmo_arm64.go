// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// Darwin (macOS ARM64) process syscall emulation: what os/exec needs beyond
// fork.

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

// Wait options.
const (
	linuxWNOHANG    = 1
	linuxWUNTRACED  = 2
	linuxWCONTINUED = 8
	appleWCONTINUED = 0x10
)

// darwinApplyFdFlags applies Linux O_CLOEXEC/O_NONBLOCK to a descriptor with
// fcntl. (The socket SOCK_CLOEXEC/SOCK_NONBLOCK flags have the same bit
// values, so the socket layer reuses this.) It talks to Apple fcntl directly
// - commands and values here are already Apple's - and calls the libc
// trampoline. This holds without the darwinCall helper, whose extra frame
// would push its deepest callers (socketpair with flags) over the nosplit
// limit.
//
//go:nosplit
func darwinApplyFdFlags(fd, flags uintptr) uintptr {
	if darwinFns.Fcntl == 0 {
		return darwinENOSYS
	}
	if flags&linuxO_CLOEXEC != 0 {
		if int64(darwinLibcCallVariadic1(darwinFns.Fcntl, fd, fcntlF_SETFD, fdCLOEXEC)) == -1 {
			return darwinErrno()
		}
	}
	if flags&linuxO_NONBLOCK != 0 {
		fl := darwinLibcCallVariadic1(darwinFns.Fcntl, fd, fcntlF_GETFL, 0)
		if int64(fl) == -1 {
			return darwinErrno()
		}
		if int64(darwinLibcCallVariadic1(darwinFns.Fcntl, fd, fcntlF_SETFL, fl|appleO_NONBLOCK)) == -1 {
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
// not atomic with the dup. The only user between fork and exec is
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
		if _, _, e := darwinCallVariadic1(darwinFns.Fcntl, fd, fcntlF_SETFD, fdCLOEXEC); e != 0 {
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

// darwinWait4 emulates wait4(2): option flags translate, and the SIGNAL
// NUMBERS in the wait status are rewritten from Apple to Linux. The status
// ENCODING is identical on both systems, so only those fields change and
// syscall.WaitStatus decodes as the process expects.
//
// The rusage buffer goes straight to Apple wait4 and is fixed up IN PLACE.
// struct rusage is many bytes on both systems with every field. This holds at
// the same offset EXCEPT tv_usec in both timevals, which Apple declares
// int32-plus-padding where Linux has int64.
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
