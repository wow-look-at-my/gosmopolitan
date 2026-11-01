// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"bytes"
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
// emulates it over ReadDirectoryChangesW.
func checkInotify() {
	dir, err := os.MkdirTemp("", "rp-inotify")
	if err != nil {
		fail("inotify", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	fd, _, errno := syscall.RawSyscall(syscall.SYS_INOTIFY_INIT1, syscall.IN_CLOEXEC|syscall.IN_NONBLOCK, 0, 0)
	if errno != 0 {
		fail("inotify", "inotify_init1: %v", errno)
		return
	}
	reader := &inotifyReader{file: os.NewFile(fd, "inotify")}
	defer reader.file.Close()

	path, err := syscall.BytePtrFromString(dir)
	if err != nil {
		fail("inotify", "path: %v", err)
		return
	}
	mask := uintptr(syscall.IN_CREATE | syscall.IN_MODIFY | syscall.IN_MOVE | syscall.IN_DELETE)
	if _, _, errno := syscall.Syscall(syscall.SYS_INOTIFY_ADD_WATCH, fd, uintptr(unsafe.Pointer(path)), mask); errno != 0 {
		fail("inotify", "inotify_add_watch: %v", errno)
		return
	}

	child := filepath.Join(dir, "child")
	if err := os.WriteFile(child, nil, 0o600); err != nil {
		fail("inotify", "create: %v", err)
		return
	}
	if err := reader.await(syscall.IN_CREATE, "child"); err != nil {
		fail("inotify", "%v", err)
		return
	}
	if err := os.WriteFile(child, []byte("text"), 0o600); err != nil {
		fail("inotify", "write: %v", err)
		return
	}
	if err := reader.await(syscall.IN_MODIFY, "child"); err != nil {
		fail("inotify", "%v", err)
		return
	}
	moved := filepath.Join(dir, "moved")
	if err := os.Rename(child, moved); err != nil {
		fail("inotify", "rename: %v", err)
		return
	}
	if err := reader.await(syscall.IN_MOVED_FROM, "child"); err != nil {
		fail("inotify", "%v", err)
		return
	}
	if err := reader.await(syscall.IN_MOVED_TO, "moved"); err != nil {
		fail("inotify", "%v", err)
		return
	}
	if err := os.Remove(moved); err != nil {
		fail("inotify", "remove: %v", err)
		return
	}
	if err := reader.await(syscall.IN_DELETE, "moved"); err != nil {
		fail("inotify", "%v", err)
		return
	}
	ok("inotify", "create, write, rename and delete reported")
}

// An inotifyReader keeps what one read returned past the record await wanted.
type inotifyReader struct {
	file *os.File
	rest []byte
}

// await consumes records until one matches.
func (reader *inotifyReader) await(mask uint32, name string) error {
	if err := reader.file.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("deadline: %v", err)
	}
	for {
		for len(reader.rest) >= syscall.SizeofInotifyEvent {
			event := (*syscall.InotifyEvent)(unsafe.Pointer(&reader.rest[0]))
			end := syscall.SizeofInotifyEvent + int(event.Len)
			text := bytes.TrimRight(reader.rest[syscall.SizeofInotifyEvent:end], "\x00")
			reader.rest = reader.rest[end:]
			if event.Mask == mask && string(text) == name {
				return nil
			}
		}
		buf := make([]byte, 4096)
		count, err := reader.file.Read(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return fmt.Errorf("no event %#x %q within the deadline", mask, name)
		}
		if err != nil {
			return fmt.Errorf("read: %v", err)
		}
		reader.rest = buf[:count]
	}
}
