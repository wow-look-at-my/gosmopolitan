// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime_test

import (
	. "runtime"
	"testing"
)

// sigPairs is the authoritative Linux<->Apple signal correspondence,
// written out pair by pair from upstream defs_linux_arm64.go (which
// defs_cosmo_arm64.go mirrors for 1..31) and defs_darwin_arm64.go.
// The runtime tables must match it exactly.
var sigPairs = map[uint32]uint32{ // linux -> apple
	1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6, // HUP INT QUIT ILL TRAP ABRT
	7:  10, // BUS
	8:  8,  // FPE
	9:  9,  // KILL
	10: 30, // USR1
	11: 11, // SEGV
	12: 31, // USR2
	13: 13, // PIPE
	14: 14, // ALRM
	15: 15, // TERM
	17: 20, // CHLD
	18: 19, // CONT
	19: 17, // STOP
	20: 18, // TSTP
	21: 21, // TTIN
	22: 22, // TTOU
	23: 16, // URG
	24: 24, // XCPU
	25: 25, // XFSZ
	26: 26, // VTALRM
	27: 27, // PROF
	28: 28, // WINCH
	29: 23, // IO
	31: 12, // SYS
}

// Linux signals with no Apple equivalent, and vice versa.
var linuxOnly = []uint32{16, 30} // STKFLT, PWR
var appleOnly = []uint32{7, 29}  // EMT, INFO

func TestCosmoSigXlatTables(t *testing.T) {
	for l, a := range sigPairs {
		if got := CosmoSigL2A(l); got != a {
			t.Errorf("CosmoSigL2A(%d) = %d, want %d", l, got, a)
		}
		if got := CosmoSigA2L(a); got != l {
			t.Errorf("CosmoSigA2L(%d) = %d, want %d", a, got, l)
		}
	}
	for _, l := range linuxOnly {
		if got := CosmoSigL2A(l); got != 0 {
			t.Errorf("CosmoSigL2A(%d) = %d, want 0 (no Apple equivalent)", l, got)
		}
	}
	for _, a := range appleOnly {
		if got := CosmoSigA2L(a); got != 0 {
			t.Errorf("CosmoSigA2L(%d) = %d, want 0 (no Linux equivalent)", a, got)
		}
	}
	// Out-of-range and zero inputs must translate to 0, never index
	// out of the tables.
	for _, s := range []uint32{0, 32, 33, 64, 65, 128, 1 << 30} {
		if got := CosmoSigL2A(s); got != 0 {
			t.Errorf("CosmoSigL2A(%d) = %d, want 0", s, got)
		}
		if got := CosmoSigA2L(s); got != 0 {
			t.Errorf("CosmoSigA2L(%d) = %d, want 0", s, got)
		}
	}
}

func TestCosmoSigXlatRoundTrip(t *testing.T) {
	// Every mapped Linux signal must round-trip exactly, and every
	// mapped Apple signal must round-trip exactly (the tables are
	// mutually inverse bijections on their mapped domains).
	for l := uint32(1); l <= 64; l++ {
		if a := CosmoSigL2A(l); a != 0 {
			if back := CosmoSigA2L(a); back != l {
				t.Errorf("round trip: L2A(%d)=%d but A2L(%d)=%d", l, a, a, back)
			}
		}
	}
	for a := uint32(1); a <= 31; a++ {
		if l := CosmoSigA2L(a); l != 0 {
			if back := CosmoSigL2A(l); back != a {
				t.Errorf("round trip: A2L(%d)=%d but L2A(%d)=%d", a, l, l, back)
			}
		}
	}
}

func TestCosmoSigmaskXlat(t *testing.T) {
	// A mask of every mapped Linux signal converts to a mask of every
	// mapped Apple signal and back without loss.
	var lm uint64
	var am uint32
	for l, a := range sigPairs {
		lm |= 1 << (l - 1)
		am |= 1 << (a - 1)
	}
	if got := CosmoSigmaskL2A(lm); got != am {
		t.Errorf("CosmoSigmaskL2A(%#x) = %#x, want %#x", lm, got, am)
	}
	if got := CosmoSigmaskA2L(am); got != lm {
		t.Errorf("CosmoSigmaskA2L(%#x) = %#x, want %#x", am, got, lm)
	}

	// All-ones Linux mask: unmapped bits (STKFLT, PWR, realtime range)
	// are dropped, everything else lands on its Apple bit.
	if got := CosmoSigmaskL2A(^uint64(0)); got != am {
		t.Errorf("CosmoSigmaskL2A(all ones) = %#x, want %#x", got, am)
	}
	// All-ones Apple mask: EMT and INFO are dropped.
	if got := CosmoSigmaskA2L(^uint32(0)); got != lm {
		t.Errorf("CosmoSigmaskA2L(all ones) = %#x, want %#x", got, lm)
	}

	// Single-bit spot checks across diverging numbers.
	if got := CosmoSigmaskL2A(1 << (10 - 1)); got != 1<<(30-1) { // SIGUSR1
		t.Errorf("SIGUSR1 mask: got %#x, want %#x", got, uint32(1<<29))
	}
	if got := CosmoSigmaskA2L(1 << (16 - 1)); got != 1<<(23-1) { // SIGURG
		t.Errorf("SIGURG mask: got %#x, want %#x", got, uint64(1<<22))
	}
	if got := CosmoSigmaskL2A(0); got != 0 {
		t.Errorf("CosmoSigmaskL2A(0) = %#x, want 0", got)
	}
	if got := CosmoSigmaskA2L(0); got != 0 {
		t.Errorf("CosmoSigmaskA2L(0) = %#x, want 0", got)
	}
}
