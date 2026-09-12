// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (darwin || dragonfly || freebsd || netbsd || openbsd) && !cosmo

package net

import "syscall"

// bsdIffMulticast is this kernel's own IFF_MULTICAST. cosmo cannot read
// it from package syscall, where the name carries the Linux value; see
// interface_cosmo.go.
const bsdIffMulticast = syscall.IFF_MULTICAST

func interfaceTable(ifindex int) ([]Interface, error) {
	return bsdInterfaceTable(ifindex)
}

func interfaceAddrTable(ifi *Interface) ([]Addr, error) {
	return bsdInterfaceAddrTable(ifi)
}
