// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime_test

import (
	"runtime"
	"testing"
)

// The DNS server list is read by walking raw offsets into a buffer
// iphlpapi filled, and a wrong offset reads plausible garbage rather
// than failing: the walk would hand net a nameserver that is not one,
// on the one host where net has no other source. Only a Windows host
// executes it, so these are the numbers documented for win64, pinned.
func TestNTFixedInfoLayout(t *testing.T) {
	if got := runtime.NTFixedInfoDNSList; got != 272 {
		t.Errorf("FIXED_INFO.DnsServerList offset = %d, want 272 (HostName[132] + DomainName[132] + CurrentDnsServer ptr)", got)
	}
	if got := runtime.NTIPAddrStringAddr; got != 8 {
		t.Errorf("IP_ADDR_STRING.IpAddress offset = %d, want 8 (past the Next pointer)", got)
	}
	if got := runtime.NTIPAddrStringSize; got != 48 {
		t.Errorf("sizeof(IP_ADDR_STRING) = %d, want 48 (ptr + two 16-byte strings + padded DWORD)", got)
	}
	if got := runtime.NTErrorBufferOverflow; got != 111 {
		t.Errorf("ERROR_BUFFER_OVERFLOW = %d, want 111", got)
	}
}
