// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || darwin || dragonfly || freebsd || (linux && !android) || netbsd || openbsd

package os_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO opened for reading and writing is in the poller on every host: a
// read deadline expires, and a write through another descriptor wakes the
// parked reader.
func TestFIFOReadWriteDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	reader, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer reader.Close()

	buf := make([]byte, 16)
	if err := reader.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := reader.Read(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read on an empty FIFO past its deadline: %v, want %v", err, os.ErrDeadlineExceeded)
	}

	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile for writing: %v", err)
	}
	defer writer.Close()
	if err := reader.SetReadDeadline(time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	time.AfterFunc(50*time.Millisecond, func() {
		writer.WriteString("wake")
	})
	count, err := reader.Read(buf)
	if err != nil || string(buf[:count]) != "wake" {
		t.Fatalf("Read = %q, %v; want %q", buf[:count], err, "wake")
	}
}
