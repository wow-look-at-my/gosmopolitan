// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package x509

import "runtime"

// hostRootPool builds the root pool from the certificates the host hands
// over directly, and reports whether it has one.
//
// Only an NT host answers. certFiles lists paths, Windows publishes none
// of them, and the scan that follows would come back empty -- every
// certificate signed by an unknown authority. Linux and macOS do publish
// a bundle, get nil here, and reach the scan unchanged.
//
// A certificate the parser rejects is skipped rather than failing the
// pool: one bad entry in a store of hundreds must not cost the host every
// root it has.
func hostRootPool() (*CertPool, bool) {
	ders := runtime.CosmoHostRootCerts()
	if len(ders) == 0 {
		return nil, false
	}
	pool := NewCertPool()
	for _, der := range ders {
		cert, err := ParseCertificate(der)
		if err != nil {
			continue
		}
		pool.AddCert(cert)
	}
	if pool.len() == 0 {
		return nil, false
	}
	return pool, true
}
