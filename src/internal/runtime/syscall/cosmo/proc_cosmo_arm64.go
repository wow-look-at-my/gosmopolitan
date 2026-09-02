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
