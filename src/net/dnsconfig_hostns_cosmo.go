// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package net

import "runtime"

// hostNameservers asks the host for its resolvers, for a host that
// keeps them somewhere other than a file this package can open. Only
// Windows does; a cosmo binary on Linux or macOS gets nil here and
// reads /etc/resolv.conf as usual.
func hostNameservers() []string { return runtime.CosmoHostDNSServers() }
