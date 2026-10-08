// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import "unsafe"

// Image file machine values (winnt.h), as IsWow64Process2 reports them.
const (
	_NT_IMAGE_FILE_MACHINE_AMD64 = 0x8664
	_NT_IMAGE_FILE_MACHINE_ARM64 = 0xaa64
	_NT_IMAGE_FILE_MACHINE_I386  = 0x014c
)

// cosmoHostArch reports the machine this process is running on, which is not
// always the machine the payload was built for.
func cosmoHostArch() string {
	return ""
}

// cosmoHostArchNT reads the machine from IsWow64Process2, which reports
// it even for a process that is not under WOW64. Nothing calls it yet.
// It is the probe an arm64 Windows bring-up needs, kept beside the
// numbers it reads rather than rewritten from scratch then.
func cosmoHostArchNT() string {
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
