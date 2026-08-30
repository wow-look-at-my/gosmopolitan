// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && !amd64

package runtime

// The NT call machinery is amd64-only (os_cosmo_nt.go), which is the
// only architecture an APE boots on Windows. Nothing here has a host to
// ask.
func ntDNSServers() []string { return nil }
