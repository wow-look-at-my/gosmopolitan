// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// Linux <-> Apple (XNU) signal number translation.
//
// A cosmo binary thinks in LINUX signal numbers everywhere: sigtable,
// the _SIG* constants, os/signal and syscall all use them. On macOS
// hosts the kernel speaks Apple's numbering, which diverges for the
// BSD-heritage signals. Every darwin boundary therefore translates:
// Linux->Apple when installing handlers, masking, and sending
// (sigaction, sigprocmask, kill, pthread_kill, raise), Apple->Linux
// when receiving (sigtramp) and when decoding wait statuses.
//
// The tables below are derived from defs_cosmo_arm64.go (identical to
// upstream defs_linux_arm64.go for 1..31) and upstream
// defs_darwin_arm64.go:
//
//	signal      Linux  Apple        signal      Linux  Apple
//	SIGHUP        1      1          SIGCHLD      17     20
//	SIGINT        2      2          SIGCONT      18     19
//	SIGQUIT       3      3          SIGSTOP      19     17
//	SIGILL        4      4          SIGTSTP      20     18
//	SIGTRAP       5      5          SIGTTIN      21     21
//	SIGABRT       6      6          SIGTTOU      22     22
//	SIGBUS        7     10          SIGURG       23     16
//	SIGFPE        8      8          SIGXCPU      24     24
//	SIGKILL       9      9          SIGXFSZ      25     25
//	SIGUSR1      10     30          SIGVTALRM    26     26
//	SIGSEGV      11     11          SIGPROF      27     27
//	SIGUSR2      12     31          SIGWINCH     28     28
//	SIGPIPE      13     13          SIGIO        29     23
//	SIGALRM      14     14          SIGPWR       30      -
//	SIGTERM      15     15          SIGSYS       31     12
//	SIGSTKFLT    16      -          SIGEMT        -      7
//	                                SIGINFO       -     29
//
// Signals without a counterpart translate to 0: Linux SIGSTKFLT and
// SIGPWR and the realtime range (32..64) do not exist on XNU; Apple
// SIGEMT and SIGINFO have no Linux number, so this runtime never
// installs a handler for them and they keep their default action.
//
// The tables are plain initialized data (no init function) so they are
// usable from the earliest signal paths, and they are shared with the
// assembly in sys_cosmo_arm64.s (sigtramp, raise, tgkill, sigfwd),
// which indexes them directly.

// cosmoSigL2ATab maps a Linux signal number (index 1..31) to the Apple
// number, 0 if there is none. Index 0 and 32..64 are 0.
var cosmoSigL2ATab = [65]byte{
	1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6,
	7:  10, // SIGBUS
	8:  8,
	9:  9,
	10: 30, // SIGUSR1
	11: 11,
	12: 31, // SIGUSR2
	13: 13, 14: 14, 15: 15,
	16: 0,  // SIGSTKFLT: no Apple equivalent
	17: 20, // SIGCHLD
	18: 19, // SIGCONT
	19: 17, // SIGSTOP
	20: 18, // SIGTSTP
	21: 21, 22: 22,
	23: 16, // SIGURG
	24: 24, 25: 25, 26: 26, 27: 27, 28: 28,
	29: 23, // SIGIO
	30: 0,  // SIGPWR: no Apple equivalent
	31: 12, // SIGSYS
}

// cosmoSigA2LTab maps an Apple signal number (index 1..31) to the
// Linux number, 0 if there is none.
var cosmoSigA2LTab = [32]byte{
	1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6,
	7:  0, // SIGEMT: no Linux equivalent
	8:  8,
	9:  9,
	10: 7, // SIGBUS
	11: 11,
	12: 31, // SIGSYS
	13: 13, 14: 14, 15: 15,
	16: 23, // SIGURG
	17: 19, // SIGSTOP
	18: 20, // SIGTSTP
	19: 18, // SIGCONT
	20: 17, // SIGCHLD
	21: 21, 22: 22,
	23: 29, // SIGIO
	24: 24, 25: 25, 26: 26, 27: 27, 28: 28,
	29: 0,  // SIGINFO: no Linux equivalent
	30: 10, // SIGUSR1
	31: 12, // SIGUSR2
}

// cosmoSigL2A translates a Linux signal number to Apple's; 0 if the
// signal has no Apple equivalent.
//
//go:nosplit
func cosmoSigL2A(sig uint32) uint32 {
	if sig >= uint32(len(cosmoSigL2ATab)) {
		return 0
	}
	return uint32(cosmoSigL2ATab[sig])
}

// cosmoSigA2L translates an Apple signal number to Linux's; 0 if the
// signal has no Linux equivalent.
//
//go:nosplit
func cosmoSigA2L(sig uint32) uint32 {
	if sig >= uint32(len(cosmoSigA2LTab)) {
		return 0
	}
	return uint32(cosmoSigA2LTab[sig])
}

// cosmoSigmaskL2A converts a Linux 64-bit signal mask to an Apple
// 32-bit sigset_t by remapping each bit through the number table (bit
// N-1 represents signal N on both systems - the numbers differ, so
// this is a bit REMAPPING, not a truncation). Bits for signals without
// an Apple equivalent are dropped: they cannot be generated on an XNU
// host.
//
//go:nosplit
func cosmoSigmaskL2A(m uint64) uint32 {
	var a uint32
	for l := uint32(1); l <= 31; l++ {
		if m&(1<<(l-1)) != 0 {
			if ap := cosmoSigL2A(l); ap != 0 {
				a |= 1 << (ap - 1)
			}
		}
	}
	return a
}

// cosmoSigmaskA2L converts an Apple 32-bit sigset_t to a Linux 64-bit
// signal mask (inverse of cosmoSigmaskL2A).
//
//go:nosplit
func cosmoSigmaskA2L(a uint32) uint64 {
	var m uint64
	for as := uint32(1); as <= 31; as++ {
		if a&(1<<(as-1)) != 0 {
			if l := cosmoSigA2L(as); l != 0 {
				m |= 1 << (l - 1)
			}
		}
	}
	return m
}
