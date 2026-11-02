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

// inotifyContract runs what src/syscall/inotify_cosmo_test.go runs on Linux, on
// the hosts that test does not reach.
func inotifyContract() error {
	checks := []struct {
		name string
		run  func(dir string) error
	}{
		{"write held open", inotifyWriteHeldOpen},
		{"truncate", inotifyTruncate},
		{"reads", inotifyNeverFires},
		{"moved in", inotifyMovedIn},
		{"moved out", inotifyMovedOut},
		{"short-lived name", inotifyShortLived},
		{"symlink", inotifySymlinkNoFollow},
		{"read sizes", inotifyReadSizes},
		{"descriptors", inotifyDescriptors},
		{"truncates through a held descriptor", inotifyHeldTruncates},
		{"truncate by path past a held writer", inotifyTruncatePastWriter},
		{"truncate by path past a held reader", inotifyTruncatePastReader},
		{"write through a descriptor older than the watch", inotifyWriteBeforeWatch},
		{"duplicated instance", inotifyDuplicate},
		{"instance through net.FileConn", inotifyFileConn},
		{"a watch leaves the temporary directory alone", inotifyTempUntouched},
	}
	var failures []error
	for _, check := range checks {
		dir, err := os.MkdirTemp("", "rp-inotify")
		if err != nil {
			return fmt.Errorf("%s: mkdtemp: %v", check.name, err)
		}
		err = check.run(dir)
		os.RemoveAll(dir)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %v", check.name, err))
		}
	}
	return errors.Join(failures...)
}

func inotifyOpen() (*inotifyReader, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("inotify_init1: %v", err)
	}
	return &inotifyReader{file: os.NewFile(uintptr(fd), "inotify"), fd: fd}, nil
}

func (reader *inotifyReader) add(path string, mask uint32) error {
	if _, err := syscall.InotifyAddWatch(reader.fd, path, mask); err != nil {
		return fmt.Errorf("inotify_add_watch(%q): %v", path, err)
	}
	return nil
}

// quiet fails on any record that arrives before the watcher has been silent
// for a moment.
func (reader *inotifyReader) quiet() error {
	if len(reader.rest) > 0 {
		return fmt.Errorf("unexpected record %x", reader.rest)
	}
	if err := reader.file.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		return fmt.Errorf("deadline: %v", err)
	}
	buf := make([]byte, 4096)
	count, err := reader.file.Read(buf)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read: %v", err)
	}
	event := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[0]))
	return fmt.Errorf("unexpected event %#x in %q", event.Mask, buf[syscall.SizeofInotifyEvent:count])
}

func inotifyWriteHeldOpen(dir string) error {
	reader, err := inotifyOpen()
	if err != nil {
		return err
	}
	defer reader.file.Close()
	if err := reader.add(dir, syscall.IN_CREATE|syscall.IN_MODIFY|syscall.IN_CLOSE_WRITE); err != nil {
		return err
	}
	file, err := os.Create(filepath.Join(dir, "held"))
	if err != nil {
		return err
	}
	defer file.Close()
	if err := reader.await(syscall.IN_CREATE, "held"); err != nil {
		return err
	}
	if _, err := file.WriteString("text"); err != nil {
		return err
	}
	if err := reader.quiet(); err != nil {
		return fmt.Errorf("while the descriptor stayed open: %v", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := reader.await(syscall.IN_MODIFY, "held"); err != nil {
		return err
	}
	return reader.await(syscall.IN_CLOSE_WRITE, "held")
}

func inotifyTruncate(dir string) error {
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("text"), 0o600); err != nil {
		return err
	}
	reader, err := inotifyOpen()
	if err != nil {
		return err
	}
	defer reader.file.Close()
	if err := reader.add(dir, syscall.IN_MODIFY); err != nil {
		return err
	}
	if err := os.Truncate(path, 2); err != nil {
		return err
	}
	if err := reader.await(syscall.IN_MODIFY, "file"); err != nil {
		return fmt.Errorf("shrinking: %v", err)
	}
	if err := os.Truncate(path, 8); err != nil {
		return err
	}
	if err := reader.await(syscall.IN_MODIFY, "file"); err != nil {
		return fmt.Errorf("growing: %v", err)
	}
	return nil
}

func inotifyNeverFires(dir string) error {
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("text"), 0o600); err != nil {
		return err
	}
	reader, err := inotifyOpen()
	if err != nil {
		return err
	}
	defer reader.file.Close()
	mask := uint32(syscall.IN_ACCESS | syscall.IN_OPEN | syscall.IN_CLOSE_NOWRITE)
	if err := reader.add(dir, mask); err != nil {
		return err
	}
	if err := reader.add(path, mask); err != nil {
		return err
	}
	if _, err := os.ReadFile(path); err != nil {
		return err
	}
	if _, err := os.ReadDir(dir); err != nil {
		return err
	}
	return reader.quiet()
}

func inotifyMovedIn(dir string) error {
	outside, err := os.MkdirTemp("", "rp-inotify")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outside)
	source := filepath.Join(outside, "file")
	if err := os.WriteFile(source, []byte("text"), 0o600); err != nil {
		return err
	}
	reader, err := inotifyOpen()
	if err != nil {
		return err
	}
	defer reader.file.Close()
	if err := reader.add(dir, syscall.IN_CREATE|syscall.IN_MODIFY|syscall.IN_CLOSE_WRITE|syscall.IN_MOVE); err != nil {
		return err
	}
	if err := os.Rename(source, filepath.Join(dir, "file")); err != nil {
		return err
	}
	for _, mask := range []uint32{syscall.IN_CREATE, syscall.IN_MODIFY, syscall.IN_CLOSE_WRITE} {
		if err := reader.await(mask, "file"); err != nil {
			return err
		}
	}
	return reader.quiet()
}

func inotifyMovedOut(dir string) error {
	outside, err := os.MkdirTemp("", "rp-inotify")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outside)
	source := filepath.Join(dir, "file")
	if err := os.WriteFile(source, []byte("text"), 0o600); err != nil {
		return err
	}
	reader, err := inotifyOpen()
	if err != nil {
		return err
	}
	defer reader.file.Close()
	if err := reader.add(dir, syscall.IN_DELETE|syscall.IN_MOVE); err != nil {
		return err
	}
	if err := os.Rename(source, filepath.Join(outside, "file")); err != nil {
		return err
	}
	if err := reader.await(syscall.IN_DELETE, "file"); err != nil {
		return err
	}
	return reader.quiet()
}

func inotifyShortLived(dir string) error {
	reader, err := inotifyOpen()
	if err != nil {
		return err
	}
	defer reader.file.Close()
	if err := reader.add(dir, syscall.IN_CREATE|syscall.IN_DELETE); err != nil {
		return err
	}
	path := filepath.Join(dir, "brief")
	for range 20 {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := reader.await(syscall.IN_CREATE, "brief"); err != nil {
			return err
		}
		if err := reader.await(syscall.IN_DELETE, "brief"); err != nil {
			return err
		}
	}
	return nil
}

func inotifySymlinkNoFollow(dir string) error {
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("text"), 0o600); err != nil {
		return err
	}
	if err := os.Symlink(target, link); err != nil {
		return err
	}
	reader, err := inotifyOpen()
	if err != nil {
		return err
	}
	defer reader.file.Close()
	if err := reader.add(link, syscall.IN_MODIFY|syscall.IN_DELETE_SELF|syscall.IN_DONT_FOLLOW); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte("changed"), 0o600); err != nil {
		return err
	}
	if err := reader.quiet(); err != nil {
		return fmt.Errorf("a write through the link's target: %v", err)
	}
	if err := os.Remove(link); err != nil {
		return err
	}
	if err := reader.await(syscall.IN_DELETE_SELF, ""); err != nil {
		return err
	}
	return reader.await(syscall.IN_IGNORED, "")
}

func inotifyReadSizes(dir string) error {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	if _, err := syscall.InotifyAddWatch(fd, dir, syscall.IN_CREATE); err != nil {
		return err
	}
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			return err
		}
	}
	time.Sleep(200 * time.Millisecond)
	var small [syscall.SizeofInotifyEvent]byte
	if _, err := syscall.Read(fd, small[:]); err != syscall.EINVAL {
		return fmt.Errorf("a read too small for a record = %v, want EINVAL", err)
	}
	buf := make([]byte, syscall.SizeofInotifyEvent+16)
	for _, name := range []string{"first", "second"} {
		count, err := syscall.Read(fd, buf)
		if err != nil {
			return fmt.Errorf("a read that fits one record: %v", err)
		}
		if count != len(buf) || string(buf[syscall.SizeofInotifyEvent:syscall.SizeofInotifyEvent+len(name)]) != name {
			return fmt.Errorf("a read that fits one record returned %q", buf[:count])
		}
	}
	return nil
}

func inotifyDescriptors(dir string) error {
	const children = 256
	for idx := range children {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("child-%03d", idx)), nil, 0o600); err != nil {
			return err
		}
	}
	reader, err := inotifyOpen()
	if err != nil {
		return err
	}
	defer reader.file.Close()
	before, err := inotifyLowestFd(dir)
	if err != nil {
		return err
	}
	if err := reader.add(dir, syscall.IN_ALL_EVENTS); err != nil {
		return err
	}
	after, err := inotifyLowestFd(dir)
	if err != nil {
		return err
	}
	if after-before >= children/2 {
		return fmt.Errorf("a watch took the lowest free descriptor from %d to %d", before, after)
	}
	return nil
}

func inotifyLowestFd(path string) (int, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	syscall.Close(fd)
	return fd, nil
}
