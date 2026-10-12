// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package os_test

import (
	"internal/poll"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// go-ipc parks a waiter on a FIFO read with a deadline, so a FIFO must join
// the runtime poller on every host. The poller's own error names the cause.
func TestFIFOJoinsThePoller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	pfd := &poll.FD{Sysfd: fd, IsStream: true, ZeroReadIsEOF: true}
	if err := pfd.Init("file", true); err != nil {
		syscall.Close(fd)
		t.Fatalf("the runtime poller refused a FIFO: %v", err)
	}
	pfd.Close()

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer file.Close()
	if err := file.SetReadDeadline(time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SetReadDeadline on a FIFO: %v", err)
	}
}
