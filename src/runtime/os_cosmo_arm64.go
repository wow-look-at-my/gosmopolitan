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
	dlsymNameGetpid  = []byte("getpid\x00")
	dlsymNameGetppid = []byte("getppid\x00")
	dlsymNameGetuid  = []byte("getuid\x00")
	dlsymNameGeteuid = []byte("geteuid\x00")
	dlsymNameGetgid  = []byte("getgid\x00")
	dlsymNameGetegid = []byte("getegid\x00")
	dlsymNameUmask   = []byte("umask\x00")
)

// osArchInit resolves darwin host functions at startup and hands them to
// the cosmo syscall package's darwin emulation. It runs from osinit, on
// the system stack, before any user code and before the first fork, so
// dlsym (which may take dyld locks) is safe here.
func osArchInit() {
	if !isdarwin() {
		return
	}
	cosmoDarwinGetpidFn = cosmoDlsym(&dlsymNameGetpid[0])
	cosmo.SetDarwinFns(&cosmo.DarwinFns{
		Getpid:      cosmoDarwinGetpidFn,
		Getppid:     cosmoDlsym(&dlsymNameGetppid[0]),
		Getuid:      cosmoDlsym(&dlsymNameGetuid[0]),
		Geteuid:     cosmoDlsym(&dlsymNameGeteuid[0]),
		Getgid:      cosmoDlsym(&dlsymNameGetgid[0]),
		Getegid:     cosmoDlsym(&dlsymNameGetegid[0]),
		Umask:       cosmoDlsym(&dlsymNameUmask[0]),
		PthreadSelf: __syslib.pthread_self,
	})
}
