// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// checkXattr sets, lists and reads a user xattr, through a symlink and on it. macOS answered each of these ENOSYS until the darwin
// emulation grew them, and a copier read that as "this file has no xattrs". NT has no xattr, so there the steps are recorded.
func checkXattr() {
	s := &softStep{name: "xattr", soft: cosmoHostOS() == "windows"}
	dir, err := os.MkdirTemp("", "rp-xattr")
	if err != nil {
		fail("xattr", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		fail("xattr", "create: %v", err)
		return
	}
	link := filepath.Join(dir, "l")
	if err := os.Symlink("f", link); err != nil {
		fail("xattr", "symlink: %v", err)
		return
	}
	const name, value = "user.runtimeprobe", "probe-value"
	if !s.do("Setxattr", syscall.Setxattr(path, name, []byte(value), 0)) {
		s.finish("")
		return
	}
	buf := make([]byte, 256)
	if n, err := syscall.Listxattr(path, buf); s.do("Listxattr", err) && !bytes.Contains(buf[:n], []byte(name+"\x00")) {
		s.do("Listxattr names", os.ErrNotExist)
	}
	if n, err := syscall.Getxattr(path, name, buf); s.do("Getxattr", err) && string(buf[:n]) != value {
		s.do("Getxattr value", os.ErrInvalid)
	}
	// Through the link the attribute is the target's. With the l-prefixed call it is the link's own, which has none.
	if n, err := syscall.Getxattr(link, name, buf); s.do("Getxattr via link", err) && string(buf[:n]) != value {
		s.do("Getxattr via link value", os.ErrInvalid)
	}
	if n, err := llistxattr(link, buf); s.do("Llistxattr", err) && bytes.Contains(buf[:n], []byte(name)) {
		s.do("Llistxattr follows the link", os.ErrInvalid)
	}
	s.finish("")
}

// llistxattr is the l-prefixed call that package syscall does not wrap.
func llistxattr(path string, dest []byte) (int, error) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return 0, err
	}
	r, _, errno := syscall.Syscall(syscall.SYS_LLISTXATTR, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&dest[0])), uintptr(len(dest)))
	if errno != 0 {
		return 0, errno
	}
	return int(r), nil
}
