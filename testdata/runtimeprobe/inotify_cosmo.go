// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

// checkInotify watches a directory the way fsnotify does: a nonblocking
// instance read through the runtime poller, with the calls x/sys/unix makes. A
// Linux host serves it, a macOS host emulates it over kqueue, and an NT host
// has no emulation and answers ENOSYS.
func checkInotify() {
	fd, _, errno := syscall.RawSyscall(syscall.SYS_INOTIFY_INIT1, syscall.IN_CLOEXEC|syscall.IN_NONBLOCK, 0, 0)
	if cosmoHostOS() == "windows" {
		if errno != syscall.ENOSYS {
			fail("inotify", "inotify_init1 on NT = %v, want ENOSYS", errno)
			return
		}
		ok("inotify", "ENOSYS on NT")
		return
	}
	if errno != 0 {
		fail("inotify", "inotify_init1: %v", errno)
		return
	}
	file := os.NewFile(fd, "inotify")
	defer file.Close()

	dir, err := os.MkdirTemp("", "rp-inotify")
	if err != nil {
		fail("inotify", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	path, err := syscall.BytePtrFromString(dir)
	if err != nil {
		fail("inotify", "path: %v", err)
		return
	}
	mask := uintptr(syscall.IN_CREATE | syscall.IN_DELETE)
	if _, _, errno := syscall.Syscall(syscall.SYS_INOTIFY_ADD_WATCH, fd, uintptr(unsafe.Pointer(path)), mask); errno != 0 {
		fail("inotify", "inotify_add_watch: %v", errno)
		return
	}

	child := filepath.Join(dir, "child")
	if err := os.WriteFile(child, nil, 0o600); err != nil {
		fail("inotify", "create: %v", err)
		return
	}
	if err := awaitInotify(file, syscall.IN_CREATE, "child"); err != nil {
		fail("inotify", "%v", err)
		return
	}
	if err := os.Remove(child); err != nil {
		fail("inotify", "remove: %v", err)
		return
	}
	if err := awaitInotify(file, syscall.IN_DELETE, "child"); err != nil {
		fail("inotify", "%v", err)
		return
	}
	ok("inotify", "create and delete reported")
}

// awaitInotify reads records until one matches.
func awaitInotify(file *os.File, mask uint32, name string) error {
	if err := file.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("deadline: %v", err)
	}
	buf := make([]byte, 4096)
	for {
		count, err := file.Read(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return fmt.Errorf("no event %#x %q within the deadline", mask, name)
		}
		if err != nil {
			return fmt.Errorf("read: %v", err)
		}
		for rest := buf[:count]; len(rest) >= syscall.SizeofInotifyEvent; {
			event := (*syscall.InotifyEvent)(unsafe.Pointer(&rest[0]))
			text := rest[syscall.SizeofInotifyEvent : syscall.SizeofInotifyEvent+int(event.Len)]
			if event.Mask == mask && string(text[:len(name)]) == name {
				return nil
			}
			rest = rest[syscall.SizeofInotifyEvent+int(event.Len):]
		}
	}
}
