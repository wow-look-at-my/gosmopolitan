// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import "unsafe"

// CosmoHostOS returns the operating system the process is running on:
// "linux", "darwin", "windows", or "unknown" for a host this runtime has no
// port for.
func CosmoHostOS() string {
	switch __hostos {
	case _HOSTLINUX:
		return "linux"
	case _HOSTXNU:
		return "darwin"
	case _HOSTWINDOWS:
		return "windows"
	}
	return "unknown"
}

//go:nosplit
func hostIsDarwin() bool { return isdarwin() }

//go:nosplit
func hostIsLinux() bool { return __hostos == _HOSTLINUX }

//go:nosplit
func hostIsWindows() bool { return iswindows() }

// CosmoHostname returns the host's name, or "" when this host keeps it
// somewhere the caller can already read.
func CosmoHostname() string {
	if __hostos != _HOSTXNU {
		return ""
	}
	return cosmoDarwinHostname()
}

// CosmoHostDNSServers returns the nameservers the host has configured, as
// textual addresses.
func CosmoHostDNSServers() []string {
	if __hostos != _HOSTWINDOWS {
		return nil
	}
	return ntDNSServers()
}

// CosmoHostRootCerts returns the DER bytes of the certificates the host
// trusts as roots.
func CosmoHostRootCerts() [][]byte {
	if __hostos != _HOSTWINDOWS {
		return nil
	}
	return ntRootCerts()
}

// CosmoDarwinSysctl issues Apple's sysctl with a numeric MIB and reports how
// many bytes it wrote.
//
//go:linkname syscall_cosmoDarwinSysctl syscall.cosmoDarwinSysctl
func syscall_cosmoDarwinSysctl(mib []uint32, out []byte) (int, bool) {
	return CosmoDarwinSysctl(mib, out)
}

func CosmoDarwinSysctl(mib []uint32, out []byte) (int, bool) {
	if __hostos != _HOSTXNU || len(mib) == 0 {
		return 0, false
	}
	n := uintptr(len(out))
	var p unsafe.Pointer
	if len(out) > 0 {
		p = unsafe.Pointer(&out[0])
	}
	if cosmoDarwinSysctlCall(&mib[0], uint32(len(mib)), p, &n, nil, 0) != 0 {
		return 0, false
	}
	return int(n), true
}
