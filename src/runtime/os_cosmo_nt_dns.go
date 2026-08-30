// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// Where an NT host keeps its nameservers.
//
// net's resolv.conf reader is constrained !windows, and cosmo is not
// windows, so a cosmo binary compiles it on every host. NT has no
// /etc/resolv.conf: the read fails, net falls back to defaultNS, and
// every lookup goes to localhost, where nothing answers. The list has
// to come from Windows instead.
//
// iphlpapi's GetNetworkParams is the smallest way to ask. One call
// fills a FIXED_INFO, and its DnsServerList arrives as NUL-terminated
// dotted quads rather than sockaddrs, so nothing here parses an
// address. It reports IPv4 servers only, which is what the resolver
// needs to stop asking localhost.
//
// The library is optional, like ntdll and bcryptprimitives above it: a
// host without it degrades to no servers, never a crash.

// FIXED_INFO and IP_ADDR_STRING, win64. Spelled as the sum of the
// members rather than as a total, because a total is a number nobody
// can check; dns_cosmo_nt_test.go pins the sums against the layout
// iphlpapi actually writes.
const (
	// FIXED_INFO opens with two name buffers, then the
	// CurrentDnsServer pointer, then DnsServerList inline.
	_NT_HOSTNAME_FIELD      = 128 + 4 // MAX_HOSTNAME_LEN + 4
	_NT_DOMAINNAME_FIELD    = 128 + 4 // MAX_DOMAIN_NAME_LEN + 4
	_NT_PTR                 = 8
	_NT_FIXED_INFO_DNS_LIST = _NT_HOSTNAME_FIELD + _NT_DOMAINNAME_FIELD + _NT_PTR

	// IP_ADDR_STRING is a Next pointer, two IP_ADDRESS_STRINGs, and a
	// DWORD Context padded out to a pointer multiple.
	_NT_IP_ADDRESS_STRING   = 16 // char String[4*4]
	_NT_IP_ADDR_STRING_ADDR = _NT_PTR
	_NT_IP_ADDR_STRING_SIZE = _NT_PTR + 2*_NT_IP_ADDRESS_STRING + _NT_PTR

	_NT_ERROR_BUFFER_OVERFLOW = 111
)

var (
	ntNameIphlpapi         = []byte("iphlpapi.dll\x00")
	ntNameGetNetworkParams = []byte("GetNetworkParams\x00")

	// ntDNSReady: 0 = untried, 1 = ready, 2 = unavailable (sticky).
	ntDNSReady           uint32
	ntDNSLock            mutex
	ntGetNetworkParamsFn uintptr
)

// ntDNSEnsure resolves GetNetworkParams once, lazily: a process that
// never resolves a name must not pay for loading iphlpapi at boot.
// Reports whether the call is available.
func ntDNSEnsure() bool {
	if atomic.Load(&ntDNSReady) == 1 {
		return true
	}
	lock(&ntDNSLock)
	if ntDNSReady != 0 {
		ready := ntDNSReady == 1
		unlock(&ntDNSLock)
		return ready
	}
	gpa := ntiat[0] // &GetProcAddress
	lla := ntiat[1] // &LoadLibraryA

	if lib := ntcall(lla, uintptr(unsafe.Pointer(&ntNameIphlpapi[0])), 0, 0, 0, 0, 0); lib != 0 {
		ntGetNetworkParamsFn = ntcall(gpa, lib, uintptr(unsafe.Pointer(&ntNameGetNetworkParams[0])), 0, 0, 0, 0)
	}
	if ntGetNetworkParamsFn != 0 {
		atomic.Store(&ntDNSReady, 1)
	} else {
		atomic.Store(&ntDNSReady, 2)
	}
	unlock(&ntDNSLock)
	return ntGetNetworkParamsFn != 0
}

// ntDNSServers returns the host's configured nameservers as dotted
// quads. An empty result means the question could not be answered, and
// the caller keeps whatever it had.
func ntDNSServers() []string {
	if !ntDNSEnsure() {
		return nil
	}
	// A nil buffer asks how much room the answer needs; the call then
	// reports ERROR_BUFFER_OVERFLOW and writes the size.
	var size uint32
	if rc := ntcall(ntGetNetworkParamsFn, 0, uintptr(unsafe.Pointer(&size)), 0, 0, 0, 0); rc != _NT_ERROR_BUFFER_OVERFLOW {
		return nil
	}
	if size < _NT_FIXED_INFO_DNS_LIST+_NT_IP_ADDR_STRING_SIZE {
		return nil
	}
	buf := make([]byte, size)
	rc := ntcall(ntGetNetworkParamsFn, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)), 0, 0, 0, 0)
	if rc != 0 {
		return nil
	}

	// DnsServerList is the first entry, inline; the rest hang off it.
	// Every pointer in the chain addresses this buffer, which the call
	// sized for exactly that.
	var servers []string
	entry := unsafe.Pointer(&buf[_NT_FIXED_INFO_DNS_LIST])
	for entry != nil {
		addr := (*byte)(unsafe.Add(entry, _NT_IP_ADDR_STRING_ADDR))
		if s := gostring(addr); s != "" {
			servers = append(servers, s)
		}
		entry = *(*unsafe.Pointer)(entry)
	}
	KeepAlive(buf)
	return servers
}
