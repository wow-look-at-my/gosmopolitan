// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build darwin && !cosmo

package net

// Only the multicast reader needs a shim here.

func interfaceMulticastAddrTable(ifi *Interface) ([]Addr, error) {
	return bsdInterfaceMulticastAddrTable(ifi)
}
