// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

// These drive the pure part of the process-control emulation, which no host
// reaches without an NT kernel behind it. The tiers are cosmo libc's
// (libc/proc/setpriority-nt.c), so the table below is that file's.

package runtime_test

import (
	"runtime"
	"testing"
)

func TestNtPriorityClassFollowsLibc(t *testing.T) {
	for _, tc := range []struct {
		nice  int32
		class uintptr
	}{
		{-20, runtime.NtRealtimePriorityClass},
		{-15, runtime.NtRealtimePriorityClass},
		{-14, runtime.NtHighPriorityClass},
		{-9, runtime.NtHighPriorityClass},
		{-8, runtime.NtAboveNormalPriorityClass},
		{-3, runtime.NtAboveNormalPriorityClass},
		{-2, runtime.NtNormalPriorityClass},
		{3, runtime.NtNormalPriorityClass},
		{4, runtime.NtBelowNormalPriorityClass},
		{12, runtime.NtBelowNormalPriorityClass},
		{13, runtime.NtIdlePriorityClass},
		{20, runtime.NtIdlePriorityClass},
	} {
		if got := runtime.NtPriorityClass(tc.nice); got != tc.class {
			t.Errorf("ntPriorityClass(%d) = %#x, want %#x", tc.nice, got, tc.class)
		}
	}
}

// A class reads back as the nice value setpriority-nt.c answers with. A caller
// that sets one and reads it back therefore sees the move.
func TestNtNiceReadsEachClass(t *testing.T) {
	for _, tc := range []struct {
		class uintptr
		nice  int32
	}{
		{runtime.NtRealtimePriorityClass, -16},
		{runtime.NtHighPriorityClass, -10},
		{runtime.NtAboveNormalPriorityClass, -5},
		{runtime.NtNormalPriorityClass, 0},
		{runtime.NtBelowNormalPriorityClass, 5},
		{runtime.NtIdlePriorityClass, 15},
	} {
		if got := runtime.NtNice(tc.class); got != tc.nice {
			t.Errorf("ntNice(%#x) = %d, want %d", tc.class, got, tc.nice)
		}
	}
	// A class outside the table is not one this host set.
	if got := runtime.NtNice(0x1234); got != 0 {
		t.Errorf("ntNice(0x1234) = %d, want 0", got)
	}
}

// Every nice value the setter can choose reads back as a nice value. That
// value chooses the same class, so the round trip settles.
func TestNtPriorityClassRoundTrips(t *testing.T) {
	for nice := int32(-20); nice <= 20; nice++ {
		class := runtime.NtPriorityClass(nice)
		if again := runtime.NtPriorityClass(runtime.NtNice(class)); again != class {
			t.Errorf("ntPriorityClass(%d) is %#x, which reads back as %#x",
				nice, class, again)
		}
	}
}

// A refused call names the reason where the status says one.
func TestNtStatusErrnoNamesTheRefusalsItKnows(t *testing.T) {
	for _, tc := range []struct {
		status uint32
		want   uintptr
	}{
		{0xC0000022, 1}, // STATUS_ACCESS_DENIED is EPERM
		{0xC000010A, 3}, // STATUS_PROCESS_IS_TERMINATING is ESRCH
		{0xC0000001, 5}, // a status this host has no name for is EIO
	} {
		if got := runtime.NtStatusErrno(int32(tc.status)); got != tc.want {
			t.Errorf("ntStatusErrno(%#x) = %d, want %d", tc.status, got, tc.want)
		}
	}
}
