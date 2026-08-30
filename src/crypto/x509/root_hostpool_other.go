// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package x509

// Every other port either reads a bundle off disk or verifies through
// the platform, so nothing hands root certificates over this way.
func hostRootPool() (*CertPool, bool) { return nil, false }
