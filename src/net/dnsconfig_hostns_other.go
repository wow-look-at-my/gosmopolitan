// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows && !cosmo

package net

// Every host this file builds for publishes its resolvers in
// /etc/resolv.conf, which dnsReadConfig reads directly.
func hostNameservers() []string { return nil }
