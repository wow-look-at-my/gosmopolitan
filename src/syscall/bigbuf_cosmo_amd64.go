// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package syscall

import "unsafe"

// statfs and fstatfs are shared (bigbuf_cosmo.go): the amd64 emulation
// serves them through raw XNU statfs64/fstatfs64, with the same
// buffer-size guard the arm64 path applies.
//
// uname takes a different route on each architecture. XNU has no uname
// syscall, so arm64 resolves Apple's libc uname by name through dlsym.
// The Syslib that dlsym comes from is built by the ARM64 APE loader, so
// amd64 cannot reach it and dispatches by number instead. What it CAN
// reach is sysctl, and sysctl is where Apple's own uname reads every
// field it returns. Asking those MIBs reads the system's identity from
// the same place libc reads it.

// The sysctl MIBs Apple's uname reads, from XNU's sys/sysctl.h: the two
// top-level namespaces, then the leaf under each.
const (
	darwinCTL_KERN = 1
	darwinCTL_HW   = 6

	darwinKERN_OSTYPE    = 1
	darwinKERN_OSRELEASE = 2
	darwinKERN_VERSION   = 4
	darwinKERN_HOSTNAME  = 10

	darwinHW_MACHINE = 1
)

// darwinSysctlBufSize bounds one field. kern.version is the long one, a
// multi-line build banner, and Apple gives a utsname field 256 bytes.
// This buffer is larger than either, so a field arrives whole and
// darwinUtsField does the truncating.
const darwinSysctlBufSize = 512

func darwinUname(buf *Utsname) error {
	*buf = Utsname{}
	// Domainname stays empty, as it does on the arm64 path: Apple's
	// utsname has no such field, and no sysctl serves what a Linux kernel
	// puts there.
	fields := [...]struct {
		mib [2]int32
		dst *[65]byte
	}{
		{[2]int32{darwinCTL_KERN, darwinKERN_OSTYPE}, &buf.Sysname},
		{[2]int32{darwinCTL_KERN, darwinKERN_HOSTNAME}, &buf.Nodename},
		{[2]int32{darwinCTL_KERN, darwinKERN_OSRELEASE}, &buf.Release},
		{[2]int32{darwinCTL_KERN, darwinKERN_VERSION}, &buf.Version},
		{[2]int32{darwinCTL_HW, darwinHW_MACHINE}, &buf.Machine},
	}
	for i := range fields {
		if err := darwinSysctlString(&fields[i].mib, fields[i].dst); err != nil {
			return err
		}
	}
	return nil
}

// darwinSysctlString reads one string-valued sysctl into a Linux utsname
// field. A refused MIB fails the whole call: a half-filled utsname hands
// the caller an empty field it cannot tell from a real one.
func darwinSysctlString(mib *[2]int32, dst *[65]byte) error {
	var out [darwinSysctlBufSize]byte
	// oldlenp is read AND written: it carries the buffer size in and the
	// byte count back, so it has to be an addressable size_t.
	n := uintptr(len(out))
	_, _, e := Syscall6(SYS__SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)),
		uintptr(unsafe.Pointer(&out[0])), uintptr(unsafe.Pointer(&n)),
		0, 0)
	if e != 0 {
		return errnoErr(e)
	}
	// A value that filled the buffer with no terminator still ends at the
	// buffer. darwinUtsField stops at whichever comes first.
	if n > uintptr(len(out)) {
		n = uintptr(len(out))
	}
	darwinUtsField(dst, out[:n])
	return nil
}
