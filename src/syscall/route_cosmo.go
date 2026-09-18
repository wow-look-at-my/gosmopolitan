// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import _ "unsafe" // for linkname

// The routing MIB, as every BSD numbers it. An APE carries these for the
// Darwin host it may boot on; a Linux host never reaches this file's
// caller, because netlink answers there.
const (
	CTL_NET = 4

	// Apple's AF_ROUTE. It is NOT the Linux constant of that number:
	// Linux calls 17 AF_PACKET and has no AF_ROUTE at all. This MIB is
	// read by a Darwin kernel, so it carries Darwin's numbering.
	darwinAFRoute = 17

	NET_RT_DUMP    = 1
	NET_RT_FLAGS   = 2
	NET_RT_IFLIST  = 3
	NET_RT_IFLIST2 = 6
)

// Implemented in the runtime, which is the only package that can reach
// Apple's libc here: it is pushed across with //go:linkname rather than
// pulled, because a pull out of the runtime is refused.
func cosmoDarwinSysctl(mib []uint32, out []byte) (int, bool)

// RouteRIB fetches the routing information base from the host, the way
// every BSD publishes it: a sysctl over the AF_ROUTE branch, sized by a
// first call that writes nothing and then read by a second.
//
// Only a Darwin host serves it. macOS keeps no netlink socket, and its
// routing table has no name for sysctlbyname to ask for, so the numeric
// MIB is the whole interface. A host that is not Darwin answers
// EAFNOSUPPORT, the same errno its netlink socket would.
//
// The table can grow between the two calls. ENOMEM is what the kernel
// says when it does, and internal/routebsd retries on exactly that.
func RouteRIB(facility, param int) ([]byte, error) {
	mib := []uint32{CTL_NET, darwinAFRoute, 0, 0, uint32(facility), uint32(param)}
	n, ok := cosmoDarwinSysctl(mib, nil)
	if !ok {
		return nil, EAFNOSUPPORT
	}
	if n == 0 {
		return nil, nil
	}
	tab := make([]byte, n)
	got, ok := cosmoDarwinSysctl(mib, tab)
	if !ok {
		// The table outgrew the size the first call reported.
		return nil, ENOMEM
	}
	return tab[:got], nil
}

// The routing-message vocabulary, with Apple's numbers. A Linux host
// never reads them: netlink answers there and RouteRIB refuses. They are
// here because internal/routebsd parses what a Darwin kernel wrote, and
// the cosmo zerrors files carry Linux's numbering, where several of
// these names either mean something else or do not exist.
const (
	AF_LINK = 0x12

	RTM_VERSION = 0x5

	RTM_IFINFO    = 0xe
	RTM_NEWMADDR  = 0xf
	RTM_DELMADDR  = 0x10
	RTM_IFINFO2   = 0x12
	RTM_NEWMADDR2 = 0x13

	RTA_IFP = 0x10

	RTAX_DST     = 0x0
	RTAX_GATEWAY = 0x1
	RTAX_NETMASK = 0x2
	RTAX_IFA     = 0x5
	RTAX_IFP     = 0x4
	RTAX_BRD     = 0x7
	RTAX_MAX     = 0x8

	SizeofIfMsghdr    = 0x70
	SizeofIfaMsghdr   = 0x14
	SizeofIfmaMsghdr  = 0x10
	SizeofIfmaMsghdr2 = 0x14
)
