// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// Where an NT host keeps its trusted roots.
// crypto/x509 finds roots by scanning a list of file paths, and the
// cosmo list holds Linux paths. macOS happens to ship
// /etc/ssl/cert.pem, so the scan lands there. NT ships none of them,
// the pool comes out empty, and every HTTPS request fails to verify.
//
// crypt32's CertOpenSystemStore reads the ROOT store the rest of the
// system trusts. Each certificate arrives as a DER blob the caller
// copies out, so nothing here parses one. This is the STORE, not NT's
// chain engine: no CTLs, no root auto-update, no disallowed list. It is
// what makes a public CA verify. The library is optional, like
// iphlpapi beside it, and a host without it reports no roots.

// CERT_CONTEXT, win64. Spelled as the sum of the members rather than as
// a total, because a total is a number nobody can check;
// certs_cosmo_nt_test.go pins the sums against the layout crypt32
// actually writes.
const (
	// CERT_CONTEXT opens with a DWORD encoding type, padded out to the
	// pointer that follows it, then the length, padded the same way.
	_NT_DWORD_PADDED           = 8
	_NT_CERT_CTX_ENCODED       = _NT_DWORD_PADDED
	_NT_CERT_CTX_ENCODED_LEN   = _NT_DWORD_PADDED + _NT_PTR
	_NT_CERT_CTX_SIZE          = _NT_DWORD_PADDED + _NT_PTR + _NT_DWORD_PADDED + 2*_NT_PTR
	_NT_CERT_STORE_CLOSE_CHECK = 0
)

var (
	ntNameCrypt32              = []byte("crypt32.dll\x00")
	ntNameCertOpenSystemStoreA = []byte("CertOpenSystemStoreA\x00")
	ntNameCertEnumCerts        = []byte("CertEnumCertificatesInStore\x00")
	ntNameCertCloseStore       = []byte("CertCloseStore\x00")
	ntNameRootStore            = []byte("ROOT\x00")

	// ntCertsReady: 0 = untried, 1 = ready, 2 = unavailable (sticky).
	ntCertsReady            uint32
	ntCertsLock             mutex
	ntCertOpenSystemStoreFn uintptr
	ntCertEnumCertsFn       uintptr
	ntCertCloseStoreFn      uintptr
)

// ntCertsEnsure resolves the crypt32 entry points once, lazily: a
// process that never opens a TLS connection must not pay for loading
// crypt32 at boot. Reports whether the calls are available.
func ntCertsEnsure() bool {
	if atomic.Load(&ntCertsReady) == 1 {
		return true
	}
	lock(&ntCertsLock)
	if ntCertsReady != 0 {
		ready := ntCertsReady == 1
		unlock(&ntCertsLock)
		return ready
	}
	gpa := ntiat[0] // &GetProcAddress
	lla := ntiat[1] // &LoadLibraryA

	if lib := ntcall(lla, uintptr(unsafe.Pointer(&ntNameCrypt32[0])), 0, 0, 0, 0, 0); lib != 0 {
		ntCertOpenSystemStoreFn = ntcall(gpa, lib, uintptr(unsafe.Pointer(&ntNameCertOpenSystemStoreA[0])), 0, 0, 0, 0)
		ntCertEnumCertsFn = ntcall(gpa, lib, uintptr(unsafe.Pointer(&ntNameCertEnumCerts[0])), 0, 0, 0, 0)
		ntCertCloseStoreFn = ntcall(gpa, lib, uintptr(unsafe.Pointer(&ntNameCertCloseStore[0])), 0, 0, 0, 0)
	}
	// All three or none: enumerating a store this cannot close leaks a
	// handle for the life of the process.
	ok := ntCertOpenSystemStoreFn != 0 && ntCertEnumCertsFn != 0 && ntCertCloseStoreFn != 0
	if ok {
		atomic.Store(&ntCertsReady, 1)
	} else {
		atomic.Store(&ntCertsReady, 2)
	}
	unlock(&ntCertsLock)
	return ok
}

// ntRootCerts returns the DER bytes of every certificate in the host's
// ROOT store. An empty result means the question could not be answered,
// and the caller keeps whatever it had.
func ntRootCerts() [][]byte {
	if !ntCertsEnsure() {
		return nil
	}
	store := ntcall(ntCertOpenSystemStoreFn, 0, uintptr(unsafe.Pointer(&ntNameRootStore[0])), 0, 0, 0, 0)
	if store == 0 {
		return nil
	}

	var roots [][]byte
	var ctx uintptr
	for {
		// Enumeration frees the context it was handed and returns the
		// next one, so the previous pointer must not be touched after.
		ctx = ntcall(ntCertEnumCertsFn, store, ctx, 0, 0, 0, 0)
		if ctx == 0 {
			break
		}
		der := *(**byte)(unsafe.Pointer(ctx + _NT_CERT_CTX_ENCODED))
		n := *(*uint32)(unsafe.Pointer(ctx + _NT_CERT_CTX_ENCODED_LEN))
		if der == nil || n == 0 {
			continue
		}
		// Copied out: this memory belongs to the store, which closes below.
		buf := make([]byte, n)
		copy(buf, unsafe.Slice(der, n))
		roots = append(roots, buf)
	}

	ntcall(ntCertCloseStoreFn, store, _NT_CERT_STORE_CLOSE_CHECK, 0, 0, 0, 0)
	return roots
}
