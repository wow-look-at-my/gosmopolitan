// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// Process, credential and resource-limit syscalls emulated on macOS with
// dlsym-resolved Apple libc entries. See syscall_cosmo_arm64.go for the
// darwinCall conventions; every handler here runs on the nosplit
// dispatch spine.

// appleRlimit matches Apple's struct rlimit, which has the same two
// uint64 fields as the Linux rlimit64 that syscall.Rlimit describes.
// Only the infinity sentinel differs; darwinabi_cosmo.go rewrites it.
type appleRlimit struct {
	Cur uint64
	Max uint64
}

// darwinPrlimit emulates prlimit64 with Apple's getrlimit/setrlimit,
// which only ever address the calling process. A pid naming another
// process fails with ENOSYS rather than silently reading this one's
// limits.
//
// Linux fills old with the value the limit had BEFORE new was applied,
// so the read happens first.
//
//go:nosplit
func darwinPrlimit(pid, res, newp, oldp uintptr) (r1, r2, errno uintptr) {
	if pid != 0 {
		self, _, e := darwinCallNoError(darwinFns.Getpid)
		if e != 0 || pid != self {
			return ^uintptr(0), 0, darwinENOSYS
		}
	}
	ares, ok := darwinXlatResource(res)
	if !ok {
		return ^uintptr(0), 0, darwinEINVAL
	}
	if oldp != 0 {
		var al appleRlimit
		if _, _, e := darwinCall(darwinFns.Getrlimit, ares, uintptr(unsafe.Pointer(&al)), 0, 0, 0, 0); e != 0 {
			return ^uintptr(0), 0, e
		}
		dst := (*appleRlimit)(unsafe.Pointer(oldp))
		dst.Cur = darwinRlimitToLinux(al.Cur)
		dst.Max = darwinRlimitToLinux(al.Max)
	}
	if newp != 0 {
		src := (*appleRlimit)(unsafe.Pointer(newp))
		al := appleRlimit{
			Cur: darwinRlimitToApple(src.Cur),
			Max: darwinRlimitToApple(src.Max),
		}
		if _, _, e := darwinCall(darwinFns.Setrlimit, ares, uintptr(unsafe.Pointer(&al)), 0, 0, 0, 0); e != 0 {
			return ^uintptr(0), 0, e
		}
	}
	return 0, 0, 0
}

// darwinGetrusage emulates getrusage. RUSAGE_SELF and RUSAGE_CHILDREN
// are 0 and -1 on both systems, and every field after the two timevals
// is a 64-bit integer in the same order, so the conversion is the
// timevals alone (DarwinRusageToLinux).
//
// Deliberately not nosplit: the 144-byte Apple struct is too much for
// the dispatch spine's budget, and nothing between fork and exec calls
// this - the same reasoning the stat family follows.
func darwinGetrusage(who, buf uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Getrusage == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if buf == 0 {
		return ^uintptr(0), 0, darwinEFAULT
	}
	var ru DarwinRusage
	if _, _, e := darwinCall(darwinFns.Getrusage, who, uintptr(unsafe.Pointer(&ru)), 0, 0, 0, 0); e != 0 {
		return ^uintptr(0), 0, e
	}
	DarwinRusageToLinux(&ru, (*LinuxRusage)(unsafe.Pointer(buf)))
	return 0, 0, 0
}

// darwinGettimeofday emulates gettimeofday. The timezone argument is
// obsolete on both systems and every caller passes nil, so a non-nil one
// is refused rather than filled with a value Apple no longer maintains.
//
//go:nosplit
func darwinGettimeofday(tv, tz uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Gettimeofday == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if tz != 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	if tv == 0 {
		// Both systems accept a nil buffer as "do nothing, succeed".
		return 0, 0, 0
	}
	var atv DarwinTimeval
	if _, _, e := darwinCall(darwinFns.Gettimeofday, uintptr(unsafe.Pointer(&atv)), 0, 0, 0, 0, 0); e != 0 {
		return ^uintptr(0), 0, e
	}
	DarwinTimevalToLinux(&atv, (*LinuxTimeval)(unsafe.Pointer(tv)))
	return 0, 0, 0
}

// darwinGetpriority emulates getpriority, whose successful result may
// legitimately be -1. The only way to tell that apart from failure is to
// zero errno first and read it back, which is exactly what Apple's own
// man page prescribes.
//
//go:nosplit
func darwinGetpriority(which, who uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Getpriority == 0 || darwinFns.Error == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	// PRIO_PROCESS/PRIO_PGRP/PRIO_USER are 0/1/2 on both systems.
	//
	// __error() returns this thread's errno slot, which libc owns and
	// the Go heap knows nothing about, so the address is stable and the
	// uintptr conversion cannot outlive a moving object.
	errp := (*int32)(unsafe.Pointer(darwinLibcCall6(darwinFns.Error, 0, 0, 0, 0, 0, 0)))
	*errp = 0
	r := int32(darwinLibcCall6(darwinFns.Getpriority, which, who, 0, 0, 0, 0))
	if r == -1 && *errp != 0 {
		return ^uintptr(0), 0, darwinErrno()
	}
	return uintptr(darwinNiceBias - int64(r)), 0, 0
}
