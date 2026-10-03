// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"syscall"
	"testing"
)

// TestCosmoIPOptionValues pins the IP-level socket options to the numbers
// Linux gives them. A cosmo binary issues the Linux setsockopt syscall, so a
// wrong number here reaches the kernel as a different option. The build
// still succeeds, so a compile does not catch it.
//
// GOOS=cosmo matches the linux build tag, so an upstream file guarded
// //go:build linux compiles for cosmo and reaches these names.
// golang.org/x/net/quic is one.
func TestCosmoIPOptionValues(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"IP_RECVTOS", syscall.IP_RECVTOS, 0xd},
		{"IP_PKTINFO", syscall.IP_PKTINFO, 0x8},
		{"IPV6_RECVTCLASS", syscall.IPV6_RECVTCLASS, 0x42},
		{"IPV6_TCLASS", syscall.IPV6_TCLASS, 0x43},
		{"IPV6_RECVPKTINFO", syscall.IPV6_RECVPKTINFO, 0x31},
		{"IPV6_PKTINFO", syscall.IPV6_PKTINFO, 0x32},
	}
	for _, one := range cases {
		if one.got != one.want {
			t.Errorf("%s = %#x, want %#x", one.name, one.got, one.want)
		}
	}
}

// TestCosmoIPOptionLevels pins the protocol levels the options above are
// passed with. An option is meaningless without the level that scopes it.
func TestCosmoIPOptionLevels(t *testing.T) {
	if syscall.IPPROTO_IP != 0x0 {
		t.Errorf("IPPROTO_IP = %#x, want 0x0", syscall.IPPROTO_IP)
	}
	if syscall.IPPROTO_IPV6 != 0x29 {
		t.Errorf("IPPROTO_IPV6 = %#x, want 0x29", syscall.IPPROTO_IPV6)
	}
}
