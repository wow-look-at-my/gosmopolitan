// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import (
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// Host OS constants (passed in X3 by APE loader)
const (
	_HOSTLINUX   = 0
	_HOSTMETAL   = 1
	_HOSTWINDOWS = 2
	_HOSTXNU     = 8
	_HOSTFREEBSD = 9
	_HOSTOPENBSD = 10
	_HOSTNETBSD  = 11
)

// Syslib magic and version must match ape-m1.c
const (
	_SYSLIB_MAGIC   = 's' | 'l'<<8 | 'i'<<16 | 'b'<<24
	_SYSLIB_VERSION = 10

	// _SYSLIB_MIN_VERSION is the oldest Syslib this runtime accepts.
	// The runtime reads fields up to sigaltstack (v5) unconditionally
	// and the darwin syscall emulation needs dlsym (v6); cosmopolitan
	// libc itself refuses to load below v8 ("MANDATORY" marker in
	// ape-m1.c), so every loader in the wild that can run cosmo
	// binaries is v8+. Requiring 8 makes everything through dlerror
	// unconditionally addressable; v9/v10 entries (pthread_cpu_
	// number_np, sysctl*) stay version-gated at their use sites.
	_SYSLIB_MIN_VERSION = 8
)

// syslib holds pointers to Apple APIs provided by the APE loader.
// This structure must match the Syslib struct in ape/ape-m1.c.
// Only used on macOS ARM64.
type syslib struct {
	magic   int32
	version int32
	// Function pointers to Apple APIs
	fork                                   uintptr // long (*fork)(void)
	pipe                                   uintptr // long (*pipe)(int[2])
	clock_gettime                          uintptr // long (*clock_gettime)(int, struct timespec *)
	nanosleep                              uintptr // long (*nanosleep)(const struct timespec *, struct timespec *)
	mmap                                   uintptr // long (*mmap)(void *, size_t, int, int, int, off_t)
	pthread_jit_write_protect_supported_np uintptr
	pthread_jit_write_protect_np           uintptr
	sys_icache_invalidate                  uintptr
	pthread_create                         uintptr
	pthread_exit                           uintptr
	pthread_kill                           uintptr
	pthread_sigmask                        uintptr
	pthread_setname_np                     uintptr
	dispatch_semaphore_create              uintptr
	dispatch_semaphore_signal              uintptr
	dispatch_semaphore_wait                uintptr
	dispatch_walltime                      uintptr
	// v2
	pthread_self              uintptr
	dispatch_release          uintptr
	raise                     uintptr
	pthread_join              uintptr
	pthread_yield_np          uintptr
	pthread_stack_min         int32
	sizeof_pthread_attr_t     int32
	pthread_attr_init         uintptr
	pthread_attr_destroy      uintptr
	pthread_attr_setstacksize uintptr
	pthread_attr_setguardsize uintptr
	// v4
	exit      uintptr
	close     uintptr
	munmap    uintptr
	openat    uintptr
	write     uintptr
	read      uintptr
	sigaction uintptr
	pselect   uintptr
	mprotect  uintptr
	// v5
	sigaltstack uintptr
	getentropy  uintptr
	sem_open    uintptr
	sem_unlink  uintptr
	sem_close   uintptr
	sem_post    uintptr
	sem_wait    uintptr
	sem_trywait uintptr
	getrlimit   uintptr
	setrlimit   uintptr
	// v6
	dlopen  uintptr
	dlsym   uintptr
	dlclose uintptr
	dlerror uintptr
	// v9
	pthread_cpu_number_np uintptr
	// v10
	sysctl          uintptr
	sysctlbyname    uintptr
	sysctlnametomib uintptr
}

// __syslib is the Syslib pointer provided by the APE loader.
// Set by rt0_cosmo_arm64.s at startup. Only valid on macOS ARM64.
//
//go:linkname __syslib
var __syslib *syslib

// __hostos indicates the host operating system.
// Set by rt0_cosmo_arm64.s at startup.
// 0=Linux, 8=XNU/macOS, 9=FreeBSD, etc.
//
//go:linkname __hostos
var __hostos int32

// isdarwin returns true if running on macOS
//
//go:nosplit
func isdarwin() bool {
	return __hostos == _HOSTXNU
}

//go:nosplit
func cputicks() int64 {
	// nanotime() is a poor approximation of CPU ticks that is enough for the profiler.
	return nanotime()
}

// libcCall calls a function pointer from the Syslib.
// Used on macOS ARM64 to call Apple APIs.
//
//go:nosplit
//go:noescape
func libcCall(fn, arg unsafe.Pointer) int64

// cosmoLibcCall6 calls a C function pointer (from the Syslib or resolved
// via dlsym) with up to six integer arguments, following the Apple ARM64
// calling convention. Implemented in sys_cosmo_arm64.s.
//
//go:nosplit
//go:noescape
func cosmoLibcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) uintptr

// _RTLD_DEFAULT is Apple's RTLD_DEFAULT dlsym pseudo-handle ((void *)-2):
// search every image loaded in the process, i.e. the loader's libSystem.
const _RTLD_DEFAULT = ^uintptr(1)

// cosmoDlsym resolves a symbol from the host's libSystem via the Syslib's
// dlsym (available since Syslib v6, 2023-11-03; the loader embedded in our
// binaries is v10). Returns 0 if dlsym is unavailable or the lookup fails.
// name must be a NUL-terminated C string.
//
// This is how the runtime obtains host functions the Syslib does not
// export (getpid and friends). The alternative - extending the embedded
// ape-m1.c Syslib struct and bumping SYSLIB_VERSION - was rejected: the
// compiled loader is cached at ${TMPDIR:-$HOME}/.ape-1.10 keyed only by
// the APE loader version string, and any existing Mach-O there (including
// one compiled from an upstream cosmopolitan binary's embedded source) is
// reused as-is, so a stale v10 loader would silently satisfy the cache and
// the new fields would never reliably exist. dlsym works with every v6+
// loader in the wild, cached or fresh.
func cosmoDlsym(name *byte) uintptr {
	lib := __syslib
	if lib == nil || lib.version < 6 || lib.dlsym == 0 {
		return 0
	}
	return cosmoLibcCall6(lib.dlsym, _RTLD_DEFAULT, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0)
}

// cosmoDarwinGetpidFn is the address of Apple libc getpid, resolved at
// startup by osArchInit (the Syslib does not export getpid). Zero when
// unresolved. Read by ·getpid in sys_cosmo_arm64.s.
var cosmoDarwinGetpidFn uintptr

var (
	dlsymNameGetpid     = []byte("getpid\x00")
	dlsymNameGetppid    = []byte("getppid\x00")
	dlsymNameGetuid     = []byte("getuid\x00")
	dlsymNameGeteuid    = []byte("geteuid\x00")
	dlsymNameGetgid     = []byte("getgid\x00")
	dlsymNameGetegid    = []byte("getegid\x00")
	dlsymNameUmask      = []byte("umask\x00")
	dlsymNameFcntl      = []byte("fcntl\x00")
	dlsymNameMkdirat    = []byte("mkdirat\x00")
	dlsymNameUnlinkat   = []byte("unlinkat\x00")
	dlsymNameRenameat   = []byte("renameat\x00")
	dlsymNameFstatat    = []byte("fstatat\x00")
	dlsymNameFstat      = []byte("fstat\x00")
	dlsymNameGetcwd     = []byte("getcwd\x00")
	dlsymNameChdir      = []byte("chdir\x00")
	dlsymNameFaccessat  = []byte("faccessat\x00")
	dlsymNameReadlinkat = []byte("readlinkat\x00")
	dlsymNameReadv      = []byte("readv\x00")
	dlsymNameWritev     = []byte("writev\x00")
	// Apple's raw directory-read syscall wrapper (what readdir uses
	// internally). Exported from libSystem; the C symbol is
	// __getdirentries64 (dlsym takes the name without the Mach-O
	// leading underscore, exactly like __error below).
	dlsymNameGetdirentries = []byte("__getdirentries64\x00")
	dlsymNameError         = []byte("__error\x00")
	dlsymNamePoll          = []byte("poll\x00")

	dlsymNameSocket      = []byte("socket\x00")
	dlsymNameSocketpair  = []byte("socketpair\x00")
	dlsymNameBind        = []byte("bind\x00")
	dlsymNameListen      = []byte("listen\x00")
	dlsymNameAccept      = []byte("accept\x00")
	dlsymNameConnect     = []byte("connect\x00")
	dlsymNameGetsockname = []byte("getsockname\x00")
	dlsymNameGetpeername = []byte("getpeername\x00")
	dlsymNameSendto      = []byte("sendto\x00")
	dlsymNameRecvfrom    = []byte("recvfrom\x00")
	dlsymNameSetsockopt  = []byte("setsockopt\x00")
	dlsymNameGetsockopt  = []byte("getsockopt\x00")
	dlsymNameShutdown    = []byte("shutdown\x00")

	dlsymNamePipe    = []byte("pipe\x00")
	dlsymNameDup2    = []byte("dup2\x00")
	dlsymNameSetsid  = []byte("setsid\x00")
	dlsymNameSetpgid = []byte("setpgid\x00")
	dlsymNameExecve  = []byte("execve\x00")
	dlsymNameWait4   = []byte("wait4\x00")
	dlsymNameKill    = []byte("kill\x00")
)

// cosmoDarwinPollFn is Apple libc poll(2), resolved at startup; the
// darwin netpoller (netpoll_cosmo_xnu.go) is built on it. Zero when
// unresolved (netpollinit then fails visibly).
var cosmoDarwinPollFn uintptr

// cosmoDarwinErrorFn is Apple's __error(), the address-of-errno function,
// resolved at startup for runtime-internal errno fetches. (The syscall
// package's darwin emulation receives its own copy via SetDarwinFns.)
var cosmoDarwinErrorFn uintptr

// cosmoDarwinFcntlFn is Apple libc fcntl, resolved at startup; used by
// the runtime's own fcntl on darwin. Zero when unresolved.
var cosmoDarwinFcntlFn uintptr

// osArchInit resolves darwin host functions at startup and hands them to
// the cosmo syscall package's darwin emulation. It runs from osinit, on
// the system stack, before any user code and before the first fork, so
// dlsym (which may take dyld locks) is safe here.
func osArchInit() {
	if !isdarwin() {
		return
	}
	cosmoCheckSyslib()
	// On XNU, Ms park on pthread primitives exactly like GOOS=darwin, so
	// sigsend must use the pipe-based sigNote instead of notewakeup
	// (sigqueue_note_cosmo_arm64.go). Set before initsig installs any
	// signal handler.
	sigNoteUsed = true
	cosmoSemaInit()
	cosmoDarwinGetpidFn = cosmoDlsym(&dlsymNameGetpid[0])
	cosmoDarwinFcntlFn = cosmoDlsym(&dlsymNameFcntl[0])
	cosmoDarwinErrorFn = cosmoDlsym(&dlsymNameError[0])
	cosmoDarwinPollFn = cosmoDlsym(&dlsymNamePoll[0])
	cosmo.SetDarwinFns(&cosmo.DarwinFns{
		Getpid:        cosmoDarwinGetpidFn,
		Getppid:       cosmoDlsym(&dlsymNameGetppid[0]),
		Getuid:        cosmoDlsym(&dlsymNameGetuid[0]),
		Geteuid:       cosmoDlsym(&dlsymNameGeteuid[0]),
		Getgid:        cosmoDlsym(&dlsymNameGetgid[0]),
		Getegid:       cosmoDlsym(&dlsymNameGetegid[0]),
		Umask:         cosmoDlsym(&dlsymNameUmask[0]),
		Fcntl:         cosmoDarwinFcntlFn,
		Mkdirat:       cosmoDlsym(&dlsymNameMkdirat[0]),
		Unlinkat:      cosmoDlsym(&dlsymNameUnlinkat[0]),
		Renameat:      cosmoDlsym(&dlsymNameRenameat[0]),
		Fstatat:       cosmoDlsym(&dlsymNameFstatat[0]),
		Fstat:         cosmoDlsym(&dlsymNameFstat[0]),
		Getcwd:        cosmoDlsym(&dlsymNameGetcwd[0]),
		Chdir:         cosmoDlsym(&dlsymNameChdir[0]),
		Faccessat:     cosmoDlsym(&dlsymNameFaccessat[0]),
		Readlinkat:    cosmoDlsym(&dlsymNameReadlinkat[0]),
		Readv:         cosmoDlsym(&dlsymNameReadv[0]),
		Writev:        cosmoDlsym(&dlsymNameWritev[0]),
		Getdirentries: cosmoDlsym(&dlsymNameGetdirentries[0]),
		Error:         cosmoDarwinErrorFn,
		Socket:        cosmoDlsym(&dlsymNameSocket[0]),
		Socketpair:    cosmoDlsym(&dlsymNameSocketpair[0]),
		Bind:          cosmoDlsym(&dlsymNameBind[0]),
		Listen:        cosmoDlsym(&dlsymNameListen[0]),
		Accept:        cosmoDlsym(&dlsymNameAccept[0]),
		Connect:       cosmoDlsym(&dlsymNameConnect[0]),
		Getsockname:   cosmoDlsym(&dlsymNameGetsockname[0]),
		Getpeername:   cosmoDlsym(&dlsymNameGetpeername[0]),
		Sendto:        cosmoDlsym(&dlsymNameSendto[0]),
		Recvfrom:      cosmoDlsym(&dlsymNameRecvfrom[0]),
		Setsockopt:    cosmoDlsym(&dlsymNameSetsockopt[0]),
		Getsockopt:    cosmoDlsym(&dlsymNameGetsockopt[0]),
		Shutdown:      cosmoDlsym(&dlsymNameShutdown[0]),
		Pipe:          cosmoDlsym(&dlsymNamePipe[0]),
		Dup2:          cosmoDlsym(&dlsymNameDup2[0]),
		Setsid:        cosmoDlsym(&dlsymNameSetsid[0]),
		Setpgid:       cosmoDlsym(&dlsymNameSetpgid[0]),
		Execve:        cosmoDlsym(&dlsymNameExecve[0]),
		Wait4:         cosmoDlsym(&dlsymNameWait4[0]),
		Kill:          cosmoDlsym(&dlsymNameKill[0]),
		PthreadSelf:   __syslib.pthread_self,
		Getentropy:    cosmoSyslibGetentropy(),
		Close:         __syslib.close,
	})
}

// cosmoSyslibGetentropy returns the Syslib getentropy pointer, present
// since Syslib v5 (2023-10-09).
func cosmoSyslibGetentropy() uintptr {
	lib := __syslib
	if lib == nil || lib.version < 5 {
		return 0
	}
	return lib.getentropy
}

// cosmoCheckSyslib dies with a clear message if the APE loader's Syslib
// is older than what this runtime needs, instead of reading past the end
// of a shorter struct (undefined behavior with confusing crashes). Runs
// from osinit, before anything else touches version-dependent fields.
// (rt0 already verified the magic before setting __hostos to XNU.)
//
// The failure write itself needs Syslib write (v4, offset 232); for a
// hypothetical pre-v4 loader the message may be lost, but the process
// still dies here rather than corrupting itself later.
func cosmoCheckSyslib() {
	lib := __syslib
	if lib != nil && lib.magic == _SYSLIB_MAGIC && lib.version >= _SYSLIB_MIN_VERSION {
		return
	}
	writeErrStr("runtime: APE loader Syslib is missing or too old (need v8+); delete the cached loader (${TMPDIR:-$HOME}/.ape-*) so it is recompiled\n")
	exit(127)
}

// mstart_stub_cosmo is the pthread_create entry point for macOS threads;
// implemented in sys_cosmo_arm64.s (Go declaration for vet/asmdecl).
func mstart_stub_cosmo()

// pipe2 creates a pipe with the given Linux O_NONBLOCK/O_CLOEXEC flags.
// On Linux hosts it is the pipe2 syscall. macOS has no pipe2 and the
// Syslib's pipe takes no flags, so the flags are applied with fcntl
// afterwards (the darwin dispatcher translates cmd/arg encodings). If
// fcntl is unavailable and flags were requested, fail with ENOSYS
// instead of silently returning descriptors without the requested
// semantics - runtime users (nonblockingPipe for the netpoller) depend
// on the flags actually being set.
//
// Errno convention matches the Linux asm path: 0 or NEGATIVE errno.
func pipe2(flags int32) (r, w int32, errno int32) {
	if !isdarwin() {
		return pipe2Linux(flags)
	}
	var fds [2]int32
	if e := cosmo_pipe_trampoline(&fds[0]); e != 0 {
		return -1, -1, e
	}
	if flags != 0 {
		const (
			_F_GETFL    = 3
			_F_SETFL    = 4
			_F_SETFD    = 2
			_FD_CLOEXEC = 1
		)
		for _, fd := range fds {
			if flags&_O_CLOEXEC != 0 {
				if _, e := fcntl(fd, _F_SETFD, _FD_CLOEXEC); e != 0 {
					goto fail
				}
			}
			if flags&_O_NONBLOCK != 0 {
				fl, e := fcntl(fd, _F_GETFL, 0)
				if e != 0 {
					goto fail
				}
				if _, e := fcntl(fd, _F_SETFL, fl|_O_NONBLOCK); e != 0 {
					goto fail
				}
			}
		}
	}
	return fds[0], fds[1], 0
fail:
	closefd(fds[0])
	closefd(fds[1])
	return -1, -1, -38 // -ENOSYS
}

//go:noescape
func pipe2Linux(flags int32) (r, w int32, errno int32)

//go:noescape
func cosmo_pipe_trampoline(fds *int32) int32

// minitProcid returns the value minit stores in m.procid: on macOS the
// FULL pthread_t from pthread_self (gettid's uint32 return would
// truncate the pointer, and pthread_kill - signalM, async preemption -
// needs the real value), on Linux the tid.
//
//go:nosplit
func minitProcid() uint64 {
	if isdarwin() {
		return uint64(cosmoLibcCall6(__syslib.pthread_self, 0, 0, 0, 0, 0, 0))
	}
	return uint64(gettid())
}

// darwinSignalM sends sig (a LINUX signal number) to mp's thread with
// pthread_kill. Signals without an Apple equivalent (the realtime
// range, e.g. sigPerThreadSyscall) are dropped: they cannot be
// delivered on an XNU host.
func darwinSignalM(mp *m, sig int) {
	asig := cosmoSigL2A(uint32(sig))
	if asig == 0 {
		return
	}
	lib := __syslib
	if lib == nil || lib.pthread_kill == 0 {
		return
	}
	cosmoLibcCall6(lib.pthread_kill, uintptr(mp.procid), uintptr(asig), 0, 0, 0, 0)
}

// cosmoDarwinPollSupported reports whether the darwin netpoller can reach
// Apple libc's poll(2) on this host.
func cosmoDarwinPollSupported() bool {
	return cosmoDarwinPollFn != 0
}

// cosmoDarwinPoll calls Apple libc poll(2). timeout is in milliseconds,
// -1 blocks indefinitely. Returns the number of ready descriptors, or
// (-1, errno) with a LINUX errno number on failure.
//
// Only converted 32-bit values leave this function: C int returns arrive
// in w0 with the upper half of x0 undefined.
func cosmoDarwinPoll(pfds *pollfd, npfds int32, timeout int32) (int32, int32) {
	if cosmoDarwinPollFn == 0 {
		return -1, 38 // ENOSYS
	}
	r := int32(cosmoLibcCall6(cosmoDarwinPollFn,
		uintptr(unsafe.Pointer(pfds)),
		uintptr(uint32(npfds)),
		uintptr(uint32(timeout)), // -1 stays -1 in the callee's w2
		0, 0, 0))
	if r < 0 {
		return -1, cosmoDarwinErrno()
	}
	return r, 0
}

// cosmoDarwinErrno fetches the calling thread's errno via Apple's
// __error() and translates it to Linux numbering. Returns EIO (5) if
// __error is unavailable (cause unknowable). Call it immediately after a
// failed libc call, before anything else can clobber errno.
//
//go:nosplit
func cosmoDarwinErrno() int32 {
	if cosmoDarwinErrorFn == 0 {
		return 5 // EIO
	}
	p := cosmoLibcCall6(cosmoDarwinErrorFn, 0, 0, 0, 0, 0, 0)
	if p == 0 {
		return 5 // EIO
	}
	apple := *(*int32)(unsafe.Pointer(p))
	return int32(cosmoXlatErrno(uintptr(uint32(apple))))
}

// cosmoXlatErrno translates a positive Apple errno to the Linux value.
// Assembly FP wrapper over cosmo_xlat_errno_r0 (sys_cosmo_arm64.s) so
// the byte table has a single definition.
//
//go:nosplit
func cosmoXlatErrno(errno uintptr) uintptr

var sysctlHwNcpu = []byte("hw.ncpu\x00")

// cosmoDarwinNumCPU returns the host's CPU count on macOS via the
// Syslib's sysctlbyname("hw.ncpu"), available since Syslib v10
// (2024-05-02; the loader embedded in our binaries is v10). Returns 0
// when unavailable so the caller can fall back.
func cosmoDarwinNumCPU() int32 {
	lib := __syslib
	if lib == nil || lib.version < 10 || lib.sysctlbyname == 0 {
		return 0
	}
	var n uint32
	sz := uintptr(unsafe.Sizeof(n))
	// sysctlbyname is sysret-wrapped by the loader: 0 on success,
	// -errno (Apple numbering) on failure.
	r := cosmoLibcCall6(lib.sysctlbyname,
		uintptr(unsafe.Pointer(&sysctlHwNcpu[0])),
		uintptr(unsafe.Pointer(&n)),
		uintptr(unsafe.Pointer(&sz)),
		0, 0, 0)
	if r != 0 {
		return 0
	}
	return int32(n)
}
