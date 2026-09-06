// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import "unsafe"

// Image file machine values (winnt.h), as IsWow64Process2 reports them.
const (
	_NT_IMAGE_FILE_MACHINE_AMD64 = 0x8664
	_NT_IMAGE_FILE_MACHINE_ARM64 = 0xaa64
	_NT_IMAGE_FILE_MACHINE_I386  = 0x014c
)

// cosmoHostArch reports the machine this process is running on, which is
// not always the machine the payload was built for. It answers "" when
// the host cannot be asked, and the caller then keeps the payload's own
// architecture.
//
// Linux and macOS need no probe: the APE boot path selects the payload
// whose architecture matches the machine, and this fork does not run
// under Rosetta. NT is the exception, because the APE carries only an
// amd64 PE header, so an ARM64 Windows machine runs the amd64 payload
// through its x86-64 emulator.
func cosmoHostArch() string {
	if !iswindows() || ntIsWow64Process2Fn == 0 {
		return ""
	}
	var process, native uint16
	// IsWow64Process2 fills native with the machine even for a process
	// that is not running under WOW64, which is exactly the question.
	r, _ := ntcallE(ntIsWow64Process2Fn, _NT_CURRENT_PROCESS,
		uintptr(unsafe.Pointer(&process)),
		uintptr(unsafe.Pointer(&native)), 0, 0, 0, 0)
	if r == 0 {
		return ""
	}
	switch native {
	case _NT_IMAGE_FILE_MACHINE_ARM64:
		return "arm64"
	case _NT_IMAGE_FILE_MACHINE_AMD64:
		return "amd64"
	case _NT_IMAGE_FILE_MACHINE_I386:
		return "386"
	}
	return ""
}
