// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo_test

import (
	"internal/runtime/syscall/cosmo"
	"testing"
)

// Linux values from runtime/defs_cosmo_amd64.go, Apple values from
// runtime/signal_cosmo_xnu_flags.go.
const (
	lSA_SIGINFO = 0x4
	lSA_ONSTACK = 0x8000000
	lSA_RESTART = 0x10000000

	aSA_ONSTACK = 0x1
	aSA_RESTART = 0x2
	aSA_SIGINFO = 0x40
)

func TestSigactionFlagXlat(t *testing.T) {
	cases := []struct {
		name  string
		linux uint64
		apple int32
	}{
		{"none", 0, 0},
		{"SA_SIGINFO", lSA_SIGINFO, aSA_SIGINFO},
		{"SA_ONSTACK", lSA_ONSTACK, aSA_ONSTACK},
		{"SA_RESTART", lSA_RESTART, aSA_RESTART},
		{"all three", lSA_SIGINFO | lSA_ONSTACK | lSA_RESTART,
			aSA_SIGINFO | aSA_ONSTACK | aSA_RESTART},
	}
	for _, tc := range cases {
		if got := cosmo.SigFlagsL2A(tc.linux); got != tc.apple {
			t.Errorf("%s: SigFlagsL2A(%#x) = %#x, want %#x", tc.name, tc.linux, got, tc.apple)
		}
		if got := cosmo.SigFlagsA2L(tc.apple); got != tc.linux {
			t.Errorf("%s: SigFlagsA2L(%#x) = %#x, want %#x", tc.name, tc.apple, got, tc.linux)
		}
	}
	// A Linux bit with no Apple counterpart is dropped rather than
	// passed through, where it would name whatever Apple uses that
	// position for. SA_RESTORER (0x4000000) is the one a raw caller
	// most plausibly sets.
	for _, fl := range []uint64{0x4000000, 0x1, 0x2, 0x40000000} {
		if got := cosmo.SigFlagsL2A(fl); got != 0 {
			t.Errorf("SigFlagsL2A(%#x) = %#x, want 0", fl, got)
		}
	}
}

func TestSigactionMaskXlat(t *testing.T) {
	// bit(n) is the mask bit for signal n: bit n-1 on both systems.
	bit := func(n uint) uint64 { return 1 << (n - 1) }

	cases := []struct {
		name  string
		linux uint64
		apple uint32
	}{
		{"empty", 0, 0},
		{"SIGHUP", bit(1), uint32(bit(1))},
		{"SIGUSR1 10->30", bit(10), uint32(bit(30))},
		{"SIGUSR2 12->31", bit(12), uint32(bit(31))},
		{"SIGBUS 7->10", bit(7), uint32(bit(10))},
		{"SIGCHLD 17->20", bit(17), uint32(bit(20))},
		{"SIGSTOP 19->17", bit(19), uint32(bit(17))},
		{"SIGSYS 31->12", bit(31), uint32(bit(12))},
		{"SIGURG|SIGIO", bit(23) | bit(29), uint32(bit(16) | bit(23))},
	}
	for _, tc := range cases {
		if got := cosmo.SigmaskL2A(tc.linux); got != tc.apple {
			t.Errorf("%s: SigmaskL2A(%#x) = %#x, want %#x", tc.name, tc.linux, got, tc.apple)
		}
		if got := cosmo.SigmaskA2L(tc.apple); got != tc.linux {
			t.Errorf("%s: SigmaskA2L(%#x) = %#x, want %#x", tc.name, tc.apple, got, tc.linux)
		}
	}

	// Signals with no Apple number are dropped, not folded onto some
	// other signal's bit.
	for _, n := range []uint{16, 30, 32, 40, 64} { // SIGSTKFLT, SIGPWR, realtime
		if got := cosmo.SigmaskL2A(bit(n)); got != 0 {
			t.Errorf("SigmaskL2A(bit %d) = %#x, want 0", n, got)
		}
	}
	// Apple SIGEMT (7) and SIGINFO (29) have no Linux number.
	for _, n := range []uint{7, 29} {
		if got := cosmo.SigmaskA2L(uint32(bit(n))); got != 0 {
			t.Errorf("SigmaskA2L(bit %d) = %#x, want 0", n, got)
		}
	}

	// A full mask survives the round trip minus exactly the two signals
	// Apple does not have.
	var full uint64
	for l := uint(1); l <= 31; l++ {
		full |= bit(l)
	}
	want := full &^ (bit(16) | bit(30))
	if got := cosmo.SigmaskA2L(cosmo.SigmaskL2A(full)); got != want {
		t.Errorf("round trip of the full mask = %#x, want %#x", got, want)
	}
}

// The Linux struct is a wire format: rt_sigaction's caller lays it out
// and the emulation reads it, so a field that moves is a silent
// corruption rather than a build failure. Layout from
// runtime.sigactiont (defs_cosmo_amd64.go, identical on arm64).
func TestLinuxSigactionLayout(t *testing.T) {
	size, handler, flags, restorer, mask := cosmo.LinuxSigactionLayout()
	for _, f := range []struct {
		name      string
		got, want uintptr
	}{
		{"size", size, 32},
		{"sa_handler", handler, 0},
		{"sa_flags", flags, 8},
		{"sa_restorer", restorer, 16},
		{"sa_mask", mask, 24},
	} {
		if f.got != f.want {
			t.Errorf("linuxSigactiont %s = %d, want %d", f.name, f.got, f.want)
		}
	}
}
