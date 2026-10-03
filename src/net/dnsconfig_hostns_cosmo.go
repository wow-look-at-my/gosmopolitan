// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package net

import "runtime"

// hostNameservers asks the host for its resolvers.
func hostNameservers() []string { return runtime.CosmoHostDNSServers() }
