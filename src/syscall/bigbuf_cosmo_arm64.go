// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// uname is the one big-buffer call that is arm64-only, so statfs sits
// in bigbuf_cosmo.go and this does not. The arm64 emulation resolves
// Apple's uname by NAME through dlsym; the amd64 one dispatches raw XNU
// numbers, and XNU has no uname syscall at all - it is a libc function
// over sysctl. See bigbuf_cosmo_amd64.go.

func darwinUname(buf *Utsname) (err error) {
	var aun cosmo.DarwinUtsname
	_, _, e := Syscall(SYS_UNAME, uintptr(unsafe.Pointer(&aun)), unsafe.Sizeof(aun), 0)
	if e != 0 {
		return errnoErr(e)
	}
	darwinUtsnameToLinux(buf, &aun)
	return nil
}
