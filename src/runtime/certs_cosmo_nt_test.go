// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime_test

import (
	"runtime"
	"testing"
)

// The root store is read by walking raw offsets into a CERT_CONTEXT
// crypt32 filled, and a wrong offset reads plausible garbage rather than
// failing: the walk would hand crypto/x509 bytes that are not a
// certificate, on the one host where x509 has no other source. Only a
// Windows host executes it, so these are the numbers documented for
// win64, pinned.
func TestNTCertContextLayout(t *testing.T) {
	if got := runtime.NTCertCtxEncoded; got != 8 {
		t.Errorf("CERT_CONTEXT.pbCertEncoded offset = %d, want 8 (past dwCertEncodingType, padded to the pointer)", got)
	}
	if got := runtime.NTCertCtxEncodedLen; got != 16 {
		t.Errorf("CERT_CONTEXT.cbCertEncoded offset = %d, want 16 (past pbCertEncoded)", got)
	}
	if got := runtime.NTCertCtxSize; got != 40 {
		t.Errorf("sizeof(CERT_CONTEXT) = %d, want 40 (padded DWORD + ptr + padded DWORD + pCertInfo + hCertStore)", got)
	}
}
