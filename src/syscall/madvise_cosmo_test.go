// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package syscall_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestMadviseSharedFileMapping follows go.etcd.io/bbolt's mmap path: a
// read-only shared mapping of a file, advised MADV_RANDOM.
func TestMadviseSharedFileMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, make([]byte, syscall.Getpagesize()), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	b, err := syscall.Mmap(int(f.Fd()), 0, syscall.Getpagesize(), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		t.Fatalf("Mmap: %v", err)
	}
	defer syscall.Munmap(b)

	advice := []struct {
		name  string
		value int
	}{
		{"MADV_NORMAL", syscall.MADV_NORMAL},
		{"MADV_RANDOM", syscall.MADV_RANDOM},
		{"MADV_SEQUENTIAL", syscall.MADV_SEQUENTIAL},
		{"MADV_WILLNEED", syscall.MADV_WILLNEED},
		{"MADV_DONTNEED", syscall.MADV_DONTNEED},
	}
	for _, a := range advice {
		if err := syscall.Madvise(b, a.value); err != nil {
			t.Errorf("Madvise(%s): %v", a.name, err)
		}
	}
}
