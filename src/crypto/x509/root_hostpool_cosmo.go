// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package x509

import "runtime"

// hostRootPool builds the root pool from the certificates the host hands
// over directly, and reports whether it has one.
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
