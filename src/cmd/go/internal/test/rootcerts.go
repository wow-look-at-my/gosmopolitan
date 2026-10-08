// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package test

import (
	"crypto/x509"
	"encoding/pem"
)

// pemRootCerts is the certificates CertPool.AppendCertsFromPEM takes from data.
func pemRootCerts(data []byte) []rootCert {
	var certs []rootCert
	for len(data) > 0 {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		certs = append(certs, rootCert{subject: cert.RawSubject, raw: cert.Raw})
	}
	return certs
}
