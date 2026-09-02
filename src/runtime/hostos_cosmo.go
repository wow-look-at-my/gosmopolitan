// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// CosmoHostOS returns the operating system the process is running on:
// "linux", "darwin", "windows", or "unknown" for a host this runtime has
// no port for. It is the authoritative answer, not a guess.
//
// GOOS=cosmo binaries report runtime.GOOS == "cosmo" on every host, so
// code that must know where it actually landed - which paths to translate,
// which of a tool's platform branches to run - has had to infer it, and
// every available inference is unreliable:
//
//   - syscall.Uname is ENOSYS on macOS-Intel and on NT (the emulation
//     dispatchers there have no case for it).
//   - filesystem probes (/System/Library/CoreServices, /proc/self) are
//     answered by whatever sandbox the process is running under. A macOS
//     seatbelt profile that denies the first probe turns a Mac into a
//     "linux" answer, silently, and only inside the sandbox - which is
//     exactly where a test suite runs.
//
// __hostos needs neither: the APE entry stub records it before any Go
// code runs, and this runtime dispatches every syscall on it. If it were
// wrong the process would already be issuing the wrong syscalls.
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
