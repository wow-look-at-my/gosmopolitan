// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// inotifyHeld makes a file and a watch of its directory.
func inotifyHeld(dir string, flag int) (*inotifyReader, *os.File, string, error) {
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("text"), 0o600); err != nil {
		return nil, nil, "", err
	}
	file, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, nil, "", err
	}
	reader, err := inotifyOpen()
	if err != nil {
		file.Close()
		return nil, nil, "", err
	}
	if err := reader.add(dir, syscall.IN_MODIFY|syscall.IN_CLOSE_WRITE); err != nil {
		reader.file.Close()
		file.Close()
		return nil, nil, "", err
	}
	return reader, file, path, nil
}

func inotifyHeldTruncates(dir string) error {
	reader, file, _, err := inotifyHeld(dir, os.O_WRONLY)
	if err != nil {
		return err
	}
	defer reader.file.Close()
	defer file.Close()
	if err := file.Truncate(8); err != nil {
		return err
	}
	if err := reader.await(syscall.IN_MODIFY, "file"); err != nil {
		return fmt.Errorf("growing: %v", err)
	}
	if err := file.Truncate(2); err != nil {
		return err
	}
	if err := reader.await(syscall.IN_MODIFY, "file"); err != nil {
		return fmt.Errorf("shrinking: %v", err)
	}
	return nil
}

func inotifyTruncatePastWriter(dir string) error {
	reader, file, path, err := inotifyHeld(dir, os.O_WRONLY)
	if err != nil {
		return err
	}
	defer reader.file.Close()
	defer file.Close()
	if err := os.Truncate(path, 8); err != nil {
		return err
	}
	return reader.await(syscall.IN_MODIFY, "file")
}

func inotifyTruncatePastReader(dir string) error {
	reader, file, path, err := inotifyHeld(dir, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer reader.file.Close()
	defer file.Close()
	if err := os.Truncate(path, 8); err != nil {
		return err
	}
	return reader.await(syscall.IN_MODIFY, "file")
}

func inotifyWriteBeforeWatch(dir string) error {
	reader, file, _, err := inotifyHeld(dir, os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return err
	}
	defer reader.file.Close()
	defer file.Close()
	if _, err := file.WriteString("more"); err != nil {
		return err
	}
	if err := reader.quiet(); err != nil {
		return fmt.Errorf("while the descriptor stayed open: %v", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := reader.await(syscall.IN_MODIFY, "file"); err != nil {
		return err
	}
	return reader.await(syscall.IN_CLOSE_WRITE, "file")
}

// inotifyDuplicate has a duplicate outlive the descriptor it came from, which
// leaves it the instance's only name.
func inotifyDuplicate(dir string) error {
	first, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return err
	}
	second, err := syscall.Dup(first)
	if err != nil {
		syscall.Close(first)
		return fmt.Errorf("dup: %v", err)
	}
	defer syscall.Close(second)
	if _, err := syscall.InotifyAddWatch(second, dir, syscall.IN_CREATE); err != nil {
		syscall.Close(first)
		return fmt.Errorf("a watch through the duplicate: %v", err)
	}
	if err := syscall.Close(first); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "name"), nil, 0o600); err != nil {
		return err
	}
	time.Sleep(200 * time.Millisecond)
	var small [syscall.SizeofInotifyEvent]byte
	if _, err := syscall.Read(second, small[:]); err != syscall.EINVAL {
		return fmt.Errorf("a read too small for a record = %v, want EINVAL", err)
	}
	buf := make([]byte, syscall.SizeofInotifyEvent+16)
	count, err := syscall.Read(second, buf)
	if err != nil {
		return fmt.Errorf("a read that fits the record: %v", err)
	}
	if count != len(buf) || string(buf[syscall.SizeofInotifyEvent:syscall.SizeofInotifyEvent+4]) != "name" {
		return fmt.Errorf("a read that fits the record returned %q", buf[:count])
	}
	return nil
}
