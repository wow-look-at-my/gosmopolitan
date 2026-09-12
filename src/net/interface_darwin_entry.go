// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin && !cosmo

package net

// Only the multicast reader needs a shim here: its name is the one this
// file's cosmo sibling has to keep free, because the netlink reader
// carries it too. See interface_cosmo.go.

func interfaceMulticastAddrTable(ifi *Interface) ([]Addr, error) {
	return bsdInterfaceMulticastAddrTable(ifi)
}
