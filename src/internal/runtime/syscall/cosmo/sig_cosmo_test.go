// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo_test

import (
	"internal/runtime/syscall/cosmo"
	"testing"
)

// sigPairs must match runtime/sigxlat_cosmo_test.go: the single
// authoritative Linux<->Apple correspondence, from upstream
// defs_linux_arm64.go and defs_darwin_arm64.go.
var sigPairs = map[uintptr]uintptr{ // linux -> apple
	1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6,
	7:  10, // BUS
	8:  8,
	9:  9,
	10: 30, // USR1
	11: 11,
	12: 31, // USR2
	13: 13, 14: 14, 15: 15,
	17: 20, // CHLD
	18: 19, // CONT
	19: 17, // STOP
	20: 18, // TSTP
	21: 21, 22: 22,
	23: 16, // URG
	24: 24, 25: 25, 26: 26, 27: 27, 28: 28,
	29: 23, // IO
	31: 12, // SYS
}

func TestDarwinSignalXlat(t *testing.T) {
	for l, a := range sigPairs {
		got, ok := cosmo.DarwinXlatSignal(l)
		if !ok || got != a {
			t.Errorf("darwinXlatSignal(%d) = %d,%v, want %d,true", l, got, ok, a)
		}
		back, ok := cosmo.DarwinXlatSignalA2L(a)
		if !ok || back != l {
			t.Errorf("darwinXlatSignalA2L(%d) = %d,%v, want %d,true", a, back, ok, l)
		}
	}
	// Signal 0 (existence probe for kill) passes through.
	if got, ok := cosmo.DarwinXlatSignal(0); !ok || got != 0 {
		t.Errorf("darwinXlatSignal(0) = %d,%v, want 0,true", got, ok)
	}
	// Unmappable numbers report false.
	for _, l := range []uintptr{16, 30, 32, 33, 64, 65} { // STKFLT, PWR, realtime
		if _, ok := cosmo.DarwinXlatSignal(l); ok {
			t.Errorf("darwinXlatSignal(%d) reported ok, want false", l)
		}
	}
	for _, a := range []uintptr{7, 29, 32, 64} { // EMT, INFO, out of range
		if _, ok := cosmo.DarwinXlatSignalA2L(a); ok {
			t.Errorf("darwinXlatSignalA2L(%d) reported ok, want false", a)
		}
	}
	// Full round trip over the whole mapped domain.
	for l := uintptr(1); l <= 64; l++ {
		if a, ok := cosmo.DarwinXlatSignal(l); ok {
			if back, ok2 := cosmo.DarwinXlatSignalA2L(a); !ok2 || back != l {
				t.Errorf("round trip: L2A(%d)=%d, A2L(%d)=%d,%v", l, a, a, back, ok2)
			}
		}
	}
}

func TestDarwinXlatWaitStatus(t *testing.T) {
	cases := []struct {
		name string
		in   uint32 // Apple-numbered status from wait4
		want uint32 // Linux-numbered status
	}{
		{"exit 0", 0x0000, 0x0000},
		{"exit 3", 0x0300, 0x0300},
		{"exit 255", 0xff00, 0xff00},
		{"killed SIGKILL", 9, 9},                        // same number
		{"killed SIGUSR1", 30, 10},                      // Apple 30 -> Linux 10
		{"killed SIGUSR2", 31, 12},                      // Apple 31 -> Linux 12
		{"killed SIGBUS+core", 10 | 0x80, 7 | 0x80},     // core flag preserved
		{"killed SIGEMT", 7, 7},                         // no Linux number: passthrough
		{"stopped SIGSTOP", 0x7f | 17<<8, 0x7f | 19<<8}, // Apple 17 -> Linux 19
		{"stopped SIGTSTP", 0x7f | 18<<8, 0x7f | 20<<8}, // Apple 18 -> Linux 20
		{"continued", 0xffff, 0xffff},
	}
	for _, tc := range cases {
		if got := cosmo.DarwinXlatWaitStatus(tc.in); got != tc.want {
			t.Errorf("%s: darwinXlatWaitStatus(%#x) = %#x, want %#x", tc.name, tc.in, got, tc.want)
		}
	}
}
