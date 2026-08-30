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
