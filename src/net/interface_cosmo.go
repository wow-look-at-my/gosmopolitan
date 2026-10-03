// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package net

import "runtime"

// One APE boots on kernels, so both readers are compiled in and the host
// picks. runtime.GOOS is the right question.
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

// bsdIffMulticast is APPLE's IFF_MULTICAST, 0x8000. package syscall carries the name with Linux's 0x1000, because netlink needs it there.
const bsdIffMulticast = 0x8000

// interfacesServedHere reports whether this HOST answers the interface table
// at all.
func interfacesServedHere() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}
