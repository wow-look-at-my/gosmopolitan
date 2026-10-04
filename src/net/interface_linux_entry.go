// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build linux && !cosmo

package net

// A linux build has one kernel under it.

func interfaceTable(ifindex int) ([]Interface, error) {
	return netlinkInterfaceTable(ifindex)
}

func interfaceAddrTable(ifi *Interface) ([]Addr, error) {
	return netlinkInterfaceAddrTable(ifi)
}

func interfaceMulticastAddrTable(ifi *Interface) ([]Addr, error) {
	return netlinkInterfaceMulticastAddrTable(ifi)
}
