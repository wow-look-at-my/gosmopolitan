// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package net

import "runtime"

// interfacesServedHere reports whether this HOST answers the interface
// table. One APE boots on three kernels and the table comes from netlink,
// which only a Linux kernel serves; macOS wants the route sysctl and NT
// wants GetAdaptersAddresses, and the cosmo syscall layer carries neither
// yet. runtime.GOOS is the right question here, because which kernel is
// under this process is exactly what decides it.
//
// The unserved hosts answer EAFNOSUPPORT out of NetlinkRIB rather than an
// empty table: a caller learns nobody asked, instead of being told the
// machine has no interfaces. docs/STUBS-INVENTORY.md carries the gap.
func interfacesServedHere() bool { return runtime.GOOS == "linux" }
