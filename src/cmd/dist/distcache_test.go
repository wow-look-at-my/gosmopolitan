// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The dist key reads dist's sources, so it needs no path to the running binary.
func TestDirDigestFollowsTheSources(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := dirDigest(dir)
	if again := dirDigest(dir); again != first {
		t.Fatalf("dirDigest of an unchanged directory moved: %s, then %s", first, again)
	}
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if edited := dirDigest(dir); edited == first {
		t.Fatalf("dirDigest kept %s after a source changed", first)
	}
}
