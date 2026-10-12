// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTestEnv checks that dist test gives the suite a GOPATH that exists and
// puts the cosmo exec wrappers first on PATH. A missing GOPATH fails the
// tests that fetch modules.
func TestTestEnv(t *testing.T) {
	t.Setenv("GOPATH", "/nonexist-gopath")
	t.Setenv("PATH", os.Getenv("PATH"))
	root := t.TempDir()

	testEnv(root)

	want := filepath.Join(root, "pkg", "gopath")
	if got := os.Getenv("GOPATH"); got != want {
		t.Errorf("GOPATH = %q, want %q", got, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Errorf("GOPATH %q is not a directory: %v", want, err)
	}
	wrappers := filepath.Join(root, "misc", "cosmo") + string(os.PathListSeparator)
	if path := os.Getenv("PATH"); !strings.HasPrefix(path, wrappers) {
		t.Errorf("PATH = %q, want it to start with %q", path, wrappers)
	}
}
