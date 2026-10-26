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
	step := &softStep{name: "xattr", soft: cosmoHostOS() == "windows"}
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
	if !step.do("Setxattr", syscall.Setxattr(path, name, []byte(value), 0)) {
		step.finish("")
		return
	}
	buf := make([]byte, 256)
	if size, err := syscall.Listxattr(path, buf); step.do("Listxattr", err) && !bytes.Contains(buf[:size], []byte(name+"\x00")) {
		step.do("Listxattr names", os.ErrNotExist)
	}
	if size, err := syscall.Getxattr(path, name, buf); step.do("Getxattr", err) && string(buf[:size]) != value {
		step.do("Getxattr value", os.ErrInvalid)
	}
	// Through the link the attribute is the target's. With the l-prefixed call it is the link's own, which has none.
	if size, err := syscall.Getxattr(link, name, buf); step.do("Getxattr via link", err) && string(buf[:size]) != value {
		step.do("Getxattr via link value", os.ErrInvalid)
	}
	if size, err := llistxattr(link, buf); step.do("Llistxattr", err) && bytes.Contains(buf[:size], []byte(name)) {
		step.do("Llistxattr follows the link", os.ErrInvalid)
	}
	step.finish("")
}

// llistxattr is the l-prefixed call that package syscall does not wrap.
func llistxattr(path string, dest []byte) (int, error) {
	ptr, err := syscall.BytePtrFromString(path)
	if err != nil {
		return 0, err
	}
	ret, _, errno := syscall.Syscall(syscall.SYS_LLISTXATTR, uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&dest[0])), uintptr(len(dest)))
	if errno != 0 {
		return 0, errno
	}
	return int(ret), nil
}
