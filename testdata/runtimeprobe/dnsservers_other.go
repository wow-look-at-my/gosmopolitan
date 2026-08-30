// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package main

// A non-cosmo build has no host to ask: it was compiled for the one it
// runs on.
func hostDNSServers() []string { return nil }
