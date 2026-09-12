// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package net

import "runtime"

// One APE boots on three kernels, so both readers are compiled in and
// the host picks. runtime.GOOS is the right question: which kernel is
// under this process is exactly what decides who can answer.
//
//   - Linux serves netlink, and nothing else does.
//   - Darwin serves the AF_ROUTE sysctl, and nothing else does.
//   - NT serves neither. It wants GetAdaptersAddresses, which is not
//     built yet, so it reports that nobody asked rather than an empty
//     table. docs/STUBS-INVENTORY.md carries that gap.
func interfaceTable(ifindex int) ([]Interface, error) {
	if runtime.GOOS == "darwin" {
		return bsdInterfaceTable(ifindex)
	}
	return netlinkInterfaceTable(ifindex)
}

func interfaceAddrTable(ifi *Interface) ([]Addr, error) {
	if runtime.GOOS == "darwin" {
		return bsdInterfaceAddrTable(ifi)
	}
	return netlinkInterfaceAddrTable(ifi)
}

func interfaceMulticastAddrTable(ifi *Interface) ([]Addr, error) {
	if runtime.GOOS == "darwin" {
		return bsdInterfaceMulticastAddrTable(ifi)
	}
	return netlinkInterfaceMulticastAddrTable(ifi)
}

// bsdIffMulticast is APPLE's IFF_MULTICAST, 0x8000. package syscall
// carries the name with Linux's 0x1000, because netlink needs it there,
// and the two kernels disagree on this one flag alone. Reading the Linux
// value out of a Darwin interface would report every multicast-capable
// interface as incapable, silently.
const bsdIffMulticast = 0x8000

// interfacesServedHere reports whether this HOST answers the interface
// table at all. NT does not yet; the four interface tests skip on it
// rather than assert against a kernel nobody asked.
func interfacesServedHere() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}
