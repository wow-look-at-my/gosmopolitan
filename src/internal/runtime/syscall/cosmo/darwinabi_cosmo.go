// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// Apple ABI facts the darwin emulation translates against, and the pure
// functions that do the translating. They live in an architecture-neutral
// file because they describe Apple's ABI rather than a machine: the
// structs and the constant numbering are the same on both Apple
// architectures, which also lets the tests run wherever cosmo tests run.
// The code that CALLS them is arm64-only (file_cosmo_arm64.go,
// proc_cosmo_arm64.go), since that is the only macOS port brought up.

// DarwinStatfs is Apple's struct statfs under the 64-bit-inode ABI.
// The syscall package allocates one of these and converts it to a Linux
// Statfs_t; see darwinStatfs for why the buffer cannot be built inside
// the emulation.
type DarwinStatfs struct {
	Bsize       uint32
	Iosize      int32
	Blocks      uint64
	Bfree       uint64
	Bavail      uint64
	Files       uint64
	Ffree       uint64
	Fsid        [2]int32
	Owner       uint32
	Type        uint32
	Flags       uint32
	Fssubtype   uint32
	Fstypename  [16]byte
	Mntonname   [1024]byte
	Mntfromname [1024]byte
	FlagsExt    uint32
	Reserved    [7]uint32
}

// DarwinUtsname is Apple's struct utsname: five _SYS_NAMELEN (256) byte
// fields. Apple has no domainname field.
type DarwinUtsname struct {
	Sysname  [256]byte
	Nodename [256]byte
	Release  [256]byte
	Version  [256]byte
	Machine  [256]byte
}

// utimensat's "leave this stamp alone" and "use the current time"
// sentinels sit in the nanosecond field. Linux encodes them as large
// positive values, Apple as small negative ones.
const (
	linuxUTIME_NOW  = 0x3fffffff
	linuxUTIME_OMIT = 0x3ffffffe
	appleUTIME_NOW  = -1
	appleUTIME_OMIT = -2
)

// darwinXlatUtimeNsec maps one Linux utimensat nanosecond field to
// Apple's. An ordinary nanosecond count passes through.
func darwinXlatUtimeNsec(nsec int64) int64 {
	switch nsec {
	case linuxUTIME_NOW:
		return appleUTIME_NOW
	case linuxUTIME_OMIT:
		return appleUTIME_OMIT
	}
	return nsec
}

// Linux RLIMIT_* numbers (asm-generic). Values 0..4 match Apple; the
// rest do not, and 10 and above have no Apple counterpart at all.
const (
	linuxRLIMIT_CPU     = 0
	linuxRLIMIT_FSIZE   = 1
	linuxRLIMIT_DATA    = 2
	linuxRLIMIT_STACK   = 3
	linuxRLIMIT_CORE    = 4
	linuxRLIMIT_RSS     = 5
	linuxRLIMIT_NPROC   = 6
	linuxRLIMIT_NOFILE  = 7
	linuxRLIMIT_MEMLOCK = 8
	linuxRLIMIT_AS      = 9
)

// Apple RLIMIT_* numbers (sys/resource.h). RLIMIT_RSS is a synonym for
// RLIMIT_AS there, so both Linux resources land on the same limit.
const (
	appleRLIMIT_CORE    = 4
	appleRLIMIT_AS      = 5
	appleRLIMIT_MEMLOCK = 6
	appleRLIMIT_NPROC   = 7
	appleRLIMIT_NOFILE  = 8
)

// "No limit" is all-ones on Linux and the largest positive value of a
// signed 64-bit rlim_t on Apple. Passing one through as the other turns
// an unlimited resource into a nonsense finite one, so both directions
// are rewritten.
const (
	linuxRLIM_INFINITY = ^uint64(0)
	appleRLIM_INFINITY = uint64(1)<<63 - 1
)

// darwinXlatResource maps a Linux RLIMIT_* number to Apple's. The second
// result is false for a resource Apple does not have (RLIMIT_LOCKS and
// everything above it), which the caller reports as EINVAL - the same
// answer Linux gives for a resource its own kernel does not know.
//
//go:nosplit
func darwinXlatResource(res uintptr) (uintptr, bool) {
	switch res {
	case linuxRLIMIT_CPU, linuxRLIMIT_FSIZE, linuxRLIMIT_DATA, linuxRLIMIT_STACK:
		return res, true // 0..3 agree
	case linuxRLIMIT_CORE:
		return appleRLIMIT_CORE, true
	case linuxRLIMIT_RSS, linuxRLIMIT_AS:
		return appleRLIMIT_AS, true
	case linuxRLIMIT_NPROC:
		return appleRLIMIT_NPROC, true
	case linuxRLIMIT_NOFILE:
		return appleRLIMIT_NOFILE, true
	case linuxRLIMIT_MEMLOCK:
		return appleRLIMIT_MEMLOCK, true
	}
	return 0, false
}

//go:nosplit
func darwinRlimitToLinux(v uint64) uint64 {
	if v == appleRLIM_INFINITY {
		return linuxRLIM_INFINITY
	}
	return v
}

//go:nosplit
func darwinRlimitToApple(v uint64) uint64 {
	if v == linuxRLIM_INFINITY {
		return appleRLIM_INFINITY
	}
	return v
}

// darwinNiceBias is what the Linux getpriority SYSCALL adds to a nice
// value so its result is never negative; libc subtracts it again. Apple
// returns the nice value itself, so the emulation applies the bias to
// give a caller on macOS the same number a Linux host returns.
const darwinNiceBias = 20
