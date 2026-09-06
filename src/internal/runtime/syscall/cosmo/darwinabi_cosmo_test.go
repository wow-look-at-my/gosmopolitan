// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cosmo_test

import (
	"internal/runtime/syscall/cosmo"
	"testing"
	"unsafe"
)

// The darwin emulation's ABI translation (darwinabi_cosmo.go) is pure
// arithmetic over Apple's numbering, identical on both cosmo
// architectures, so these tests pin it on any host. The Linux CI leg
// runs them; a macOS run proves the dlsym wiring behind them.
//
// Ground truth: Linux RLIMIT_* is the asm-generic numbering
// (include/uapi/asm-generic/resource.h), Apple's is
// bsd/sys/resource.h. Linux RLIM64_INFINITY is ~0; Apple's RLIM_INFINITY
// is the largest positive signed 64-bit value. Linux UTIME_NOW/OMIT are
// (1<<30)-1 and (1<<30)-2; Apple's are -1 and -2.

func TestDarwinXlatResource(t *testing.T) {
	// Every Linux resource Apple can serve, and what it becomes.
	for _, tc := range []struct {
		name  string
		linux uintptr
		apple uintptr
	}{
		{"CPU", 0, 0},
		{"FSIZE", 1, 1},
		{"DATA", 2, 2},
		{"STACK", 3, 3},
		{"CORE", 4, 4},
		// Apple's RLIMIT_RSS is a synonym for RLIMIT_AS, so the two
		// distinct Linux resources share one Apple limit.
		{"RSS", 5, 5},
		{"NPROC", 6, 7},
		{"NOFILE", 7, 8},
		{"MEMLOCK", 8, 6},
		{"AS", 9, 5},
	} {
		got, ok := cosmo.XlatResourceForTest(tc.linux)
		if !ok {
			t.Errorf("RLIMIT_%s (%d): not translatable, want %d", tc.name, tc.linux, tc.apple)
			continue
		}
		if got != tc.apple {
			t.Errorf("RLIMIT_%s (%d): got Apple %d, want %d", tc.name, tc.linux, got, tc.apple)
		}
	}

	// RLIMIT_LOCKS and everything above it have no Apple counterpart.
	// Reporting one as translatable would silently address the wrong
	// limit, so each must be refused.
	for res := uintptr(10); res <= 15; res++ {
		if _, ok := cosmo.XlatResourceForTest(res); ok {
			t.Errorf("Linux resource %d: translatable, want refused", res)
		}
	}
}

func TestDarwinRlimitInfinity(t *testing.T) {
	if cosmo.LinuxRlimInfinityTest != ^uint64(0) {
		t.Errorf("Linux RLIM_INFINITY = %#x, want %#x", cosmo.LinuxRlimInfinityTest, ^uint64(0))
	}
	if cosmo.AppleRlimInfinityTest != uint64(1)<<63-1 {
		t.Errorf("Apple RLIM_INFINITY = %#x, want %#x", cosmo.AppleRlimInfinityTest, uint64(1)<<63-1)
	}

	// The sentinel converts in both directions; an ordinary limit does
	// not move.
	if got := cosmo.RlimitToLinuxForTest(cosmo.AppleRlimInfinityTest); got != cosmo.LinuxRlimInfinityTest {
		t.Errorf("Apple infinity -> Linux: got %#x, want %#x", got, uint64(cosmo.LinuxRlimInfinityTest))
	}
	if got := cosmo.RlimitToAppleForTest(cosmo.LinuxRlimInfinityTest); got != cosmo.AppleRlimInfinityTest {
		t.Errorf("Linux infinity -> Apple: got %#x, want %#x", got, uint64(cosmo.AppleRlimInfinityTest))
	}
	for _, v := range []uint64{0, 1, 256, 1 << 20} {
		if got := cosmo.RlimitToLinuxForTest(v); got != v {
			t.Errorf("Apple %d -> Linux: got %d, want unchanged", v, got)
		}
		if got := cosmo.RlimitToAppleForTest(v); got != v {
			t.Errorf("Linux %d -> Apple: got %d, want unchanged", v, got)
		}
	}
}

func TestDarwinXlatUtimeNsec(t *testing.T) {
	if got := cosmo.XlatUtimeNsecForTest(cosmo.LinuxUtimeNowForTest); got != cosmo.AppleUtimeNowForTest {
		t.Errorf("UTIME_NOW: got %d, want %d", got, cosmo.AppleUtimeNowForTest)
	}
	if got := cosmo.XlatUtimeNsecForTest(cosmo.LinuxUtimeOmitForTest); got != cosmo.AppleUtimeOmitForTest {
		t.Errorf("UTIME_OMIT: got %d, want %d", got, cosmo.AppleUtimeOmitForTest)
	}
	// A real nanosecond count passes through. 999999999 is the largest
	// legal one and sits next to no sentinel.
	for _, ns := range []int64{0, 1, 500, 999999999} {
		if got := cosmo.XlatUtimeNsecForTest(ns); got != ns {
			t.Errorf("nsec %d: got %d, want unchanged", ns, got)
		}
	}
}

func TestDarwinNiceBias(t *testing.T) {
	// The Linux getpriority syscall returns 20-nice so its result is
	// never negative. A caller on macOS must see the same number.
	if cosmo.NiceBiasForTest != 20 {
		t.Errorf("nice bias = %d, want 20", cosmo.NiceBiasForTest)
	}
}

// TestDarwinStructSizes pins the Apple struct layouts the syscall
// package allocates on the emulation's behalf. A mismatch here is a
// buffer the kernel overruns, so the sizes are asserted rather than
// assumed.
func TestDarwinStructSizes(t *testing.T) {
	// struct statfs under the 64-bit-inode ABI: 2168 bytes, with
	// f_mntonname at 88 and f_mntfromname at 1112.
	var sf cosmo.DarwinStatfs
	if got := unsafe.Sizeof(sf); got != 2168 {
		t.Errorf("sizeof(DarwinStatfs) = %d, want 2168", got)
	}
	if got := unsafe.Offsetof(sf.Fsid); got != 48 {
		t.Errorf("offsetof(DarwinStatfs.Fsid) = %d, want 48", got)
	}
	if got := unsafe.Offsetof(sf.Flags); got != 64 {
		t.Errorf("offsetof(DarwinStatfs.Flags) = %d, want 64", got)
	}
	if got := unsafe.Offsetof(sf.Mntonname); got != 88 {
		t.Errorf("offsetof(DarwinStatfs.Mntonname) = %d, want 88", got)
	}
	if got := unsafe.Offsetof(sf.Fstypename); got != 72 {
		t.Errorf("offsetof(DarwinStatfs.Fstypename) = %d, want 72", got)
	}
	if got := unsafe.Offsetof(sf.Mntfromname); got != 1112 {
		t.Errorf("offsetof(DarwinStatfs.Mntfromname) = %d, want 1112", got)
	}
	if got := unsafe.Offsetof(sf.FlagsExt); got != 2136 {
		t.Errorf("offsetof(DarwinStatfs.FlagsExt) = %d, want 2136", got)
	}

	// struct utsname: five 256-byte fields, no domainname.
	var un cosmo.DarwinUtsname
	if got := unsafe.Sizeof(un); got != 1280 {
		t.Errorf("sizeof(DarwinUtsname) = %d, want 1280", got)
	}
	if got := unsafe.Offsetof(un.Machine); got != 4*256 {
		t.Errorf("offsetof(DarwinUtsname.Machine) = %d, want %d", got, 4*256)
	}

	// The two rusage structs are the same 144 bytes, which is what makes
	// the difference easy to miss: Apple's microsecond field is 32 bits
	// with padding behind it, Linux's is 64. A forwarded buffer therefore
	// keeps its size and loses its meaning.
	var dru cosmo.DarwinRusage
	var lru cosmo.LinuxRusage
	if got := unsafe.Sizeof(dru); got != 144 {
		t.Errorf("sizeof(DarwinRusage) = %d, want 144", got)
	}
	if got := unsafe.Sizeof(lru); got != 144 {
		t.Errorf("sizeof(LinuxRusage) = %d, want 144", got)
	}
	if got := unsafe.Sizeof(cosmo.DarwinTimeval{}); got != 16 {
		t.Errorf("sizeof(DarwinTimeval) = %d, want 16", got)
	}
	if got := unsafe.Offsetof(dru.Maxrss); got != 32 {
		t.Errorf("offsetof(DarwinRusage.Maxrss) = %d, want 32", got)
	}
	if got := unsafe.Offsetof(lru.Maxrss); got != 32 {
		t.Errorf("offsetof(LinuxRusage.Maxrss) = %d, want 32", got)
	}
}

// TestDarwinRusageToLinux pins the one thing the conversion has to get
// right: the microseconds survive the width change, and every counter
// after the timevals lands in the same field it started in. A memcpy
// would pass a size check and fail this.
func TestDarwinRusageToLinux(t *testing.T) {
	src := cosmo.DarwinRusage{
		Utime:  cosmo.DarwinTimeval{Sec: 12, Usec: 999999},
		Stime:  cosmo.DarwinTimeval{Sec: 34, Usec: 1},
		Maxrss: 1, Ixrss: 2, Idrss: 3, Isrss: 4,
		Minflt: 5, Majflt: 6, Nswap: 7, Inblock: 8,
		Oublock: 9, Msgsnd: 10, Msgrcv: 11, Nsignals: 12,
		Nvcsw: 13, Nivcsw: 14,
	}
	var dst cosmo.LinuxRusage
	cosmo.DarwinRusageToLinux(&src, &dst)

	if dst.Utime.Sec != 12 || dst.Utime.Usec != 999999 {
		t.Errorf("Utime = %d.%06d, want 12.999999", dst.Utime.Sec, dst.Utime.Usec)
	}
	if dst.Stime.Sec != 34 || dst.Stime.Usec != 1 {
		t.Errorf("Stime = %d.%06d, want 34.000001", dst.Stime.Sec, dst.Stime.Usec)
	}
	for i, pair := range []struct {
		name      string
		got, want int64
	}{
		{"Maxrss", dst.Maxrss, 1}, {"Ixrss", dst.Ixrss, 2},
		{"Idrss", dst.Idrss, 3}, {"Isrss", dst.Isrss, 4},
		{"Minflt", dst.Minflt, 5}, {"Majflt", dst.Majflt, 6},
		{"Nswap", dst.Nswap, 7}, {"Inblock", dst.Inblock, 8},
		{"Oublock", dst.Oublock, 9}, {"Msgsnd", dst.Msgsnd, 10},
		{"Msgrcv", dst.Msgrcv, 11}, {"Nsignals", dst.Nsignals, 12},
		{"Nvcsw", dst.Nvcsw, 13}, {"Nivcsw", dst.Nivcsw, 14},
	} {
		if pair.got != pair.want {
			t.Errorf("field %d (%s) = %d, want %d", i, pair.name, pair.got, pair.want)
		}
	}
}

// TestDarwinXlatIoctl pins the request numbers against the tree's own
// tables. An ioctl request encodes direction and argument size, so the
// two systems number even the calls they share differently - and a
// request forwarded unchanged does not fail, it asks the kernel for
// whatever operation happens to carry that number there.
func TestDarwinXlatIoctl(t *testing.T) {
	for _, tc := range []struct {
		name         string
		linux, apple uintptr
	}{
		{"TIOCSCTTY", cosmo.LinuxTIOCSCTTYForTest, cosmo.AppleTIOCSCTTYForTest},
		{"TIOCGPGRP", cosmo.LinuxTIOCGPGRPForTest, cosmo.AppleTIOCGPGRPForTest},
		{"TIOCSPGRP", cosmo.LinuxTIOCSPGRPForTest, cosmo.AppleTIOCSPGRPForTest},
		{"TIOCGWINSZ", cosmo.LinuxTIOCGWINSZForTest, cosmo.AppleTIOCGWINSZForTest},
		{"TIOCSWINSZ", cosmo.LinuxTIOCSWINSZForTest, cosmo.AppleTIOCSWINSZForTest},
		{"TIOCNOTTY", cosmo.LinuxTIOCNOTTYForTest, cosmo.AppleTIOCNOTTYForTest},
	} {
		got, ok := cosmo.DarwinXlatIoctl(tc.linux)
		if !ok {
			t.Errorf("%s (%#x): not served", tc.name, tc.linux)
			continue
		}
		if got != tc.apple {
			t.Errorf("%s: %#x -> %#x, want %#x", tc.name, tc.linux, got, tc.apple)
		}
	}

	// A request outside the set must be refused rather than forwarded.
	// TCGETS is the one a terminal library reaches for first, so it is
	// the case worth naming: the termios structs and their flag bits
	// need a translation table that does not exist yet.
	for _, req := range []uintptr{0x5401 /* TCGETS */, 0x5402 /* TCSETS */, 0} {
		if _, ok := cosmo.DarwinXlatIoctl(req); ok {
			t.Errorf("request %#x reported as served; it is not", req)
		}
	}
}
