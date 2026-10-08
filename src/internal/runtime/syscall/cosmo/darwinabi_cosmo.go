// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// Apple ABI facts the darwin emulation translates against, and the pure
// functions that do the translating.

// DarwinStatfs is Apple's struct statfs under the 64-bit-inode ABI. The
// syscall package allocates one of these and converts it to a Linux Statfs_t.
// See darwinStatfs for why the buffer cannot be built inside the emulation.
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

// Apple has no domainname field.
type DarwinUtsname struct {
	Sysname  [256]byte
	Nodename [256]byte
	Release  [256]byte
	Version  [256]byte
	Machine  [256]byte
}

// LinuxFlock is Linux's struct flock on amd64 and arm64, the record every
// caller of fcntl's lock commands hands the emulation.
type LinuxFlock struct {
	Type   int16
	Whence int16
	_      [4]byte
	Start  int64
	Len    int64
	Pid    int32
	_      [4]byte
}

// DarwinFlock is Apple's struct flock.
type DarwinFlock struct {
	Start  int64
	Len    int64
	Pid    int32
	Type   int16
	Whence int16
}

// Apple's lock types. Whence needs no translation: SEEK_SET, SEEK_CUR
// and SEEK_END agree.
const (
	linuxF_RDLCK  = 0
	linuxF_WRLCK  = 1
	linuxF_UNLCK  = 2
	darwinF_RDLCK = 1
	darwinF_UNLCK = 2
	darwinF_WRLCK = 3
)

// DarwinLockType translates a Linux flock l_type to Apple's. It reports
// false for a value Apple has no lock type for, which is the whole set
// of reasons a record cannot be translated.
//
//go:nosplit
func DarwinLockType(t int16) (int16, bool) {
	switch t {
	case linuxF_RDLCK:
		return darwinF_RDLCK, true
	case linuxF_WRLCK:
		return darwinF_WRLCK, true
	case linuxF_UNLCK:
		return darwinF_UNLCK, true
	}
	return 0, false
}

// LinuxLockType is the reverse. F_GETLK answers through the record, so
// the type comes back as well as goes out.
//
//go:nosplit
func LinuxLockType(t int16) (int16, bool) {
	switch t {
	case darwinF_RDLCK:
		return linuxF_RDLCK, true
	case darwinF_WRLCK:
		return linuxF_WRLCK, true
	case darwinF_UNLCK:
		return linuxF_UNLCK, true
	}
	return 0, false
}

// utimensat's "leave this stamp alone" and "use the current time" sentinels
// sit in the nanosecond field.
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

// Linux RLIMIT_* numbers (asm-generic).
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

// "No limit" is all-ones on Linux and the largest positive value of a signed
// 64-bit rlim_t on Apple.
const (
	linuxRLIM_INFINITY = ^uint64(0)
	appleRLIM_INFINITY = uint64(1)<<63 - 1
)

// darwinXlatResource maps a Linux RLIMIT_* number to Apple's. The second
// result is false for a resource Apple does not have (RLIMIT_LOCKS and
// everything above it), which the caller reports as EINVAL. The same
// answer Linux gives for a resource its own kernel does not know.
//
//go:nosplit
func darwinXlatResource(res uintptr) (uintptr, bool) {
	switch res {
	case linuxRLIMIT_CPU, linuxRLIMIT_FSIZE, linuxRLIMIT_DATA, linuxRLIMIT_STACK:
		return res, true
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

// darwinNiceBias is what the Linux getpriority SYSCALL adds to a nice value so its result is never negative.
const darwinNiceBias = 20

// DarwinTimeval is Apple's struct timeval.
type DarwinTimeval struct {
	Sec  int64
	Usec int32
	_    int32
}

// LinuxTimeval is struct timeval as the cosmo syscall package declares it.
type LinuxTimeval struct {
	Sec  int64
	Usec int64
}

// DarwinRusage is Apple's struct rusage. Consider every field after
// both timevals. That field is a 64-bit signed integer in the same
// order Linux uses - Linux took the layout from BSD - so only the
// timevals are translated.
type DarwinRusage struct {
	Utime    DarwinTimeval
	Stime    DarwinTimeval
	Maxrss   int64
	Ixrss    int64
	Idrss    int64
	Isrss    int64
	Minflt   int64
	Majflt   int64
	Nswap    int64
	Inblock  int64
	Oublock  int64
	Msgsnd   int64
	Msgrcv   int64
	Nsignals int64
	Nvcsw    int64
	Nivcsw   int64
}

// LinuxRusage is struct rusage as the Linux kernel fills it.
type LinuxRusage struct {
	Utime    LinuxTimeval
	Stime    LinuxTimeval
	Maxrss   int64
	Ixrss    int64
	Idrss    int64
	Isrss    int64
	Minflt   int64
	Majflt   int64
	Nswap    int64
	Inblock  int64
	Oublock  int64
	Msgsnd   int64
	Msgrcv   int64
	Nsignals int64
	Nvcsw    int64
	Nivcsw   int64
}

// DarwinTimevalToLinux widens one Apple timeval into a Linux one.
func DarwinTimevalToLinux(src *DarwinTimeval, dst *LinuxTimeval) {
	dst.Sec = src.Sec
	dst.Usec = int64(src.Usec)
}

// DarwinRusageToLinux converts one Apple struct rusage into a Linux one.
func DarwinRusageToLinux(src *DarwinRusage, dst *LinuxRusage) {
	DarwinTimevalToLinux(&src.Utime, &dst.Utime)
	DarwinTimevalToLinux(&src.Stime, &dst.Stime)
	dst.Maxrss = src.Maxrss
	dst.Ixrss = src.Ixrss
	dst.Idrss = src.Idrss
	dst.Isrss = src.Isrss
	dst.Minflt = src.Minflt
	dst.Majflt = src.Majflt
	dst.Nswap = src.Nswap
	dst.Inblock = src.Inblock
	dst.Oublock = src.Oublock
	dst.Msgsnd = src.Msgsnd
	dst.Msgrcv = src.Msgrcv
	dst.Nsignals = src.Nsignals
	dst.Nvcsw = src.Nvcsw
	dst.Nivcsw = src.Nivcsw
}

// ioctl request numbers. A request encodes the direction and the size of
// its argument, so both systems number even the requests they share
// differently. Every value here is the one the tree's own tables record
// (syscall/zerrors_linux_arm64.go and syscall/zerrors_darwin_arm64.go),
// never a remembered one. A wrong request does not fail, it performs a
// DIFFERENT operation on the descriptor.
const (
	linuxTIOCSCTTY  = 0x540e
	linuxTIOCGPGRP  = 0x540f
	linuxTIOCSPGRP  = 0x5410
	linuxTIOCGWINSZ = 0x5413
	linuxTIOCSWINSZ = 0x5414
	linuxTIOCNOTTY  = 0x5422

	appleTIOCSCTTY  = 0x20007461
	appleTIOCGPGRP  = 0x40047477
	appleTIOCSPGRP  = 0x80047476
	appleTIOCGWINSZ = 0x40087468
	appleTIOCSWINSZ = 0x80087467
	appleTIOCNOTTY  = 0x20007471

	// The pty grant/unlock/name trio has no Linux spelling to translate FROM.
	appleTIOCPTYGRANT = 0x20007454
	appleTIOCPTYUNLK  = 0x20007452
	appleTIOCPTYGNAME = 0x40807453
)

// The termios requests.
const (
	linuxTCGETS  = 0x5401
	linuxTCSETS  = 0x5402
	linuxTCSETSW = 0x5403
	linuxTCSETSF = 0x5404

	appleTIOCGETA  = 0x40487413
	appleTIOCSETA  = 0x80487414
	appleTIOCSETAW = 0x80487415
	appleTIOCSETAF = 0x80487416
)

// darwinXlatTermiosIoctl maps a Linux termios request to Apple's.
//
//go:nosplit
func darwinXlatTermiosIoctl(req uintptr) (uintptr, bool) {
	switch uint32(req) {
	case linuxTCGETS:
		return appleTIOCGETA, true
	case linuxTCSETS:
		return appleTIOCSETA, true
	case linuxTCSETSW:
		return appleTIOCSETAW, true
	case linuxTCSETSF:
		return appleTIOCSETAF, true
	}
	return 0, false
}

// DarwinXlatIoctl maps a Linux ioctl request to Apple's. The second
// result is false. This holds for a request this emulation does not
// serve, which the caller reports as ENOSYS rather than passing a Linux
// number to a kernel. That kernel reads it as something else entirely.
func DarwinXlatIoctl(req uintptr) (uintptr, bool) {
	switch uint32(req) {
	case linuxTIOCSCTTY:
		return appleTIOCSCTTY, true
	case linuxTIOCGPGRP:
		return appleTIOCGPGRP, true
	case linuxTIOCSPGRP:
		return appleTIOCSPGRP, true
	case linuxTIOCGWINSZ:
		return appleTIOCGWINSZ, true
	case linuxTIOCSWINSZ:
		return appleTIOCSWINSZ, true
	case linuxTIOCNOTTY:
		return appleTIOCNOTTY, true
	case appleTIOCPTYGRANT, appleTIOCPTYUNLK, appleTIOCPTYGNAME:
		// Already Apple's, and named here so the pass-through is a decision rather than a hole.
		return req, true
	}
	return 0, false
}
