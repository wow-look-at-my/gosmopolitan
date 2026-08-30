// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"
)

// checkTLS completes a TLS handshake against an off-host endpoint and
// verifies the chain against the host's own roots.
//
// The dns check next door proved a name resolves; it says nothing about
// whether the bytes that come back can be trusted. crypto/x509 finds
// roots by scanning a list of file paths, the cosmo list holds Linux
// paths, and NT publishes none of them -- so the pool came out empty and
// every HTTPS request on a Windows host failed to verify. That is the
// same shape as the resolv.conf gap, one layer up, and it also went
// unnoticed because nothing here left the machine over TLS.
//
// Verification is the point, so this deliberately does not set
// InsecureSkipVerify: a handshake that ignores the pool would pass on
// exactly the host this check exists for.
func checkTLS() {
	roots := hostRootCount()

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", tlsProbeAddr, nil)
	if err != nil {
		// Name the root count: a handshake that failed with an empty pool
		// is a different defect from one that failed with roots in hand.
		fail("tls", "DialWithDialer(%s): %v (host roots: %s)", tlsProbeAddr, err, describeRoots(roots))
		return
	}
	defer conn.Close()

	state := conn.ConnectionState()
	if len(state.VerifiedChains) == 0 {
		fail("tls", "%s handshook but produced no verified chain (host roots: %s)", tlsProbeAddr, describeRoots(roots))
		return
	}
	issuer := state.VerifiedChains[0][len(state.VerifiedChains[0])-1].Subject.CommonName
	ok("tls", fmt.Sprintf("%s verified to %q (host roots: %s)", tlsProbeAddr, issuer, describeRoots(roots)))
}

// tlsProbeAddr is dialed by the check above. The same host the dns check
// resolves, so a red here is this port's problem rather than a second
// endpoint's.
const tlsProbeAddr = "github.com:443"

// hostRootCount reports how many roots the system pool holds, which is
// the number that was zero on NT.
func hostRootCount() int {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return -1
	}
	return len(pool.Subjects())
}

func describeRoots(n int) string {
	switch {
	case n < 0:
		return "SystemCertPool failed"
	case n == 0:
		return "EMPTY: no roots loaded"
	}
	return fmt.Sprintf("%d loaded", n)
}
