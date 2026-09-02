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
}
