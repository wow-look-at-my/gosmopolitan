// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// macOS statfs and uname: allocate the Apple-layout buffer here and
// convert. See bigbuf_cosmo.go for why the emulation cannot do it.
//
// The size argument is not decoration. The emulation refuses a buffer
// that is smaller than the Apple struct, so a caller that reached
// SYS_STATFS with a Linux Statfs_t gets EINVAL instead of a two-kilobyte
// write into 120 bytes.

func darwinStatfsPath(path string, buf *Statfs_t) (err error) {
	p, err := BytePtrFromString(path)
	if err != nil {
		return err
	}
	var ast cosmo.DarwinStatfs
	_, _, e := Syscall(SYS_STATFS, uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&ast)), unsafe.Sizeof(ast))
	if e != 0 {
		return errnoErr(e)
	}
	darwinStatfsToLinux(buf, &ast)
	return nil
}

func darwinStatfsFd(fd int, buf *Statfs_t) (err error) {
	var ast cosmo.DarwinStatfs
	_, _, e := Syscall(SYS_FSTATFS, uintptr(fd),
		uintptr(unsafe.Pointer(&ast)), unsafe.Sizeof(ast))
	if e != 0 {
		return errnoErr(e)
	}
	darwinStatfsToLinux(buf, &ast)
	return nil
}

func darwinUname(buf *Utsname) (err error) {
	var aun cosmo.DarwinUtsname
	_, _, e := Syscall(SYS_UNAME, uintptr(unsafe.Pointer(&aun)), unsafe.Sizeof(aun), 0)
	if e != 0 {
		return errnoErr(e)
	}
	darwinUtsnameToLinux(buf, &aun)
	return nil
}
