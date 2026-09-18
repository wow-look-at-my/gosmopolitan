// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

// Exports for dns_cosmo_nt_test.go: the iphlpapi FIXED_INFO /
// IP_ADDR_STRING offsets os_cosmo_nt_dns.go walks. Nothing on a Linux
// or macOS host executes that walk, so the layout is only ever checked
// by pinning it.

const (
	NTFixedInfoDNSList    = _NT_FIXED_INFO_DNS_LIST
	NTIPAddrStringAddr    = _NT_IP_ADDR_STRING_ADDR
	NTIPAddrStringSize    = _NT_IP_ADDR_STRING_SIZE
	NTErrorBufferOverflow = _NT_ERROR_BUFFER_OVERFLOW
)

// Exports for certs_cosmo_nt_test.go: the crypt32 CERT_CONTEXT offsets
// os_cosmo_nt_certs.go walks, pinned for the same reason.

const (
	NTCertCtxEncoded    = _NT_CERT_CTX_ENCODED
	NTCertCtxEncodedLen = _NT_CERT_CTX_ENCODED_LEN
	NTCertCtxSize       = _NT_CERT_CTX_SIZE
)
