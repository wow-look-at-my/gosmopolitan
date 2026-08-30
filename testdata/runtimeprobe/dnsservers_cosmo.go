// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package main

import "runtime"

// hostDNSServers is what the runtime can tell net about this host's
// resolvers. Only an NT host answers; elsewhere net reads
// /etc/resolv.conf and this is empty.
func hostDNSServers() []string { return runtime.CosmoHostDNSServers() }
