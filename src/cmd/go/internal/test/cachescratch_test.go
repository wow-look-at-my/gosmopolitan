// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunScratch pins which paths the test cache still refuses to hash. Only the
// temporary directory qualifies. Every other path a test reads is an input to
// that test, and dropping the ones outside the module root is what let a changed
// file replay a stale pass. A path that no longer resolves still has to answer,
// because a test may read a file and then remove it.
func TestRunScratch(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", os.TempDir(), err)
	}
	sep := string(filepath.Separator)

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"a file the run created under the temporary directory", filepath.Join(tmp, "TestFoo123", "fixture.txt"), true},
		{"the temporary directory itself", tmp, true},
		{"a removed path under the temporary directory", filepath.Join(tmp, "gone-9d3f", "gone.txt"), true},
		{"a system file", filepath.Join(sep, "etc", "hosts"), false},
		{"a file in another checkout", filepath.Join(sep, "srv", "shared", "config.yaml"), false},
		{"a removed path elsewhere", filepath.Join(sep, "srv", "gone-9d3f.txt"), false},
	}
	for _, c := range cases {
		if got := isRunScratch(c.path); got != c.want {
			t.Errorf("isRunScratch(%q) = %v, want %v: %s", c.path, got, c.want, c.name)
		}
	}
}
