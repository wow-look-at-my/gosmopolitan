// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// CosmoHostOS returns the operating system the process is running on:
// "linux", "darwin", "windows", or "unknown" for a host this runtime
// has no port for. It is authoritative, not a guess. The APE entry stub
// records __hostos before any Go code runs and this runtime dispatches
// every syscall on it, so a wrong answer here would already have the
// process issuing the wrong syscalls.
//
// Every other way to answer is unreliable. syscall.Uname is ENOSYS on
// macOS-Intel and on NT. A filesystem probe is answered by whatever
// sandbox the process runs under, so a seatbelt profile that denies
// /System/Library/CoreServices turns a Mac into a "linux" answer,
// silently, and only inside the sandbox - where a test suite runs.
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

// CosmoHostname returns the host's name, or "" when this host keeps it
// somewhere the caller can already read.
//
// Only a macOS host answers. Linux publishes /proc/sys/kernel/hostname
// and NT fills uname's nodename, but Apple's uname reports an empty
// nodename on a machine whose name is set only in kern.hostname, which
// is where os.Hostname reads it on a native darwin build.
func CosmoHostname() string {
	if __hostos != _HOSTXNU {
		return ""
	}
	return cosmoDarwinHostname()
}

// CosmoHostDNSServers returns the nameservers the host has configured,
// as textual addresses, or nil when this host keeps them somewhere the
// caller can already read.
//
// Only an NT host answers. Linux and macOS publish /etc/resolv.conf,
// which net reads directly; Windows publishes nothing at a path, so
// without this the resolver has no server to ask and queries localhost.
// See os_cosmo_nt_dns.go.
func CosmoHostDNSServers() []string {
	if __hostos != _HOSTWINDOWS {
		return nil
	}
	return ntDNSServers()
}

// CosmoHostRootCerts returns the DER bytes of the certificates the host
// trusts as roots, or nil when this host keeps them somewhere the caller
// can already read.
//
// Only an NT host answers. Linux and macOS publish a PEM bundle at a
// path crypto/x509 scans for; Windows publishes none, so without this the
// root pool is empty and every certificate is signed by an unknown
// authority. See os_cosmo_nt_certs.go.
func CosmoHostRootCerts() [][]byte {
	if __hostos != _HOSTWINDOWS {
		return nil
	}
	return ntRootCerts()
}
