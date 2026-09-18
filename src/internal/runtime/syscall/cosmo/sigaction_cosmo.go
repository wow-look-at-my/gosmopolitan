// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// The parts of the sigaction translation that do not depend on how the
// host is reached. amd64 issues the raw __sigaction syscall over
// Apple's KERNEL struct sigaction; arm64 calls Apple libc through the
// APE loader's Syslib over the LIBC one. Both take Apple signal
// numbers, an Apple 4-byte sigset and Apple flag values, and the
// translation between those and Linux's is the same either way.

// Linux sigaction flags (runtime/defs_cosmo_amd64.go, identical on
// arm64) and their Apple counterparts
// (runtime/signal_cosmo_xnu_flags.go). Only these three cross over; any
// other Linux bit either has no Apple counterpart or names something
// Apple uses that bit position for.
const (
	linuxSA_SIGINFO = 0x4
	linuxSA_ONSTACK = 0x8000000
	linuxSA_RESTART = 0x10000000

	appleSA_ONSTACK = 0x1
	appleSA_RESTART = 0x2
	appleSA_SIGINFO = 0x40
)

// A Linux sigset_t is 8 bytes; rt_sigaction rejects any other size.
const linuxSigsetSize = 8

// linuxSigactiont is the struct rt_sigaction takes, matching
// runtime.sigactiont in defs_cosmo_amd64.go and defs_cosmo_arm64.go.
type linuxSigactiont struct {
	handler  uintptr
	flags    uint64
	restorer uintptr
	mask     uint64
}

// sigFlagsL2A translates Linux sigaction flags to Apple's.
//
//go:nosplit
func sigFlagsL2A(fl uint64) int32 {
	var a int32
	if fl&linuxSA_SIGINFO != 0 {
		a |= appleSA_SIGINFO
	}
	if fl&linuxSA_ONSTACK != 0 {
		a |= appleSA_ONSTACK
	}
	if fl&linuxSA_RESTART != 0 {
		a |= appleSA_RESTART
	}
	return a
}

// sigFlagsA2L translates Apple sigaction flags back to Linux's.
//
//go:nosplit
func sigFlagsA2L(a int32) uint64 {
	var fl uint64
	if a&appleSA_SIGINFO != 0 {
		fl |= linuxSA_SIGINFO
	}
	if a&appleSA_ONSTACK != 0 {
		fl |= linuxSA_ONSTACK
	}
	if a&appleSA_RESTART != 0 {
		fl |= linuxSA_RESTART
	}
	return fl
}

// sigmaskL2A converts a Linux 64-bit signal mask to an Apple 32-bit
// sigset_t. Bit N-1 means signal N on both systems and the numbers
// differ, so this REMAPS bits rather than truncating them. A signal
// with no Apple number is dropped: it cannot be raised on this host.
//
//go:nosplit
func sigmaskL2A(m uint64) uint32 {
	var a uint32
	for l := uintptr(1); l <= 31; l++ {
		if m&(1<<(l-1)) == 0 {
			continue
		}
		if ap, ok := darwinXlatSignal(l); ok && ap != 0 {
			a |= 1 << (ap - 1)
		}
	}
	return a
}

// sigmaskA2L is the inverse of sigmaskL2A.
//
//go:nosplit
func sigmaskA2L(a uint32) uint64 {
	var m uint64
	for as := uintptr(1); as <= 31; as++ {
		if a&(1<<(as-1)) == 0 {
			continue
		}
		if l, ok := darwinXlatSignalA2L(as); ok && l != 0 {
			m |= 1 << (l - 1)
		}
	}
	return m
}
