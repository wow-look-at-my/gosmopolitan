// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package work

import (
	"os"
	"path/filepath"
	"testing"

	"cmd/go/internal/base"
)

// TestCosmoMergeIDNamesTheLinkedLinker pins the key a merge is cached under to
// the linker's bytes. The stamped tool ID stays the same across a linker change,
// and a key built from it alone serves one linker's APE header to another.
func TestCosmoMergeIDNamesTheLinkedLinker(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "go-toolchain")
	target := filepath.Join(dir, "app.com")
	sibling := filepath.Join(dir, "sib.com")
	for path, body := range map[string]string{exe: "linker one", target: "amd64 payload", sibling: "arm64 payload"} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base.SetSelf(exe, []string{"link"})
	t.Cleanup(base.ResetSelf)

	var builder Builder
	builder.toolIDCache.Do("link", func() string { return "stamped-and-unchanged" })
	args := []string{"-apefat", target + "," + sibling, "-o", target}

	first, err := cosmoMergeID(&builder, args, target, sibling)
	if err != nil {
		t.Fatal(err)
	}
	// FileHash memoizes by name, so the second linker is a second file.
	other := filepath.Join(dir, "go-toolchain-2")
	if err := os.WriteFile(other, []byte("linker two"), 0o755); err != nil {
		t.Fatal(err)
	}
	base.SetSelf(other, []string{"link"})
	var rebuilt Builder
	rebuilt.toolIDCache.Do("link", func() string { return "stamped-and-unchanged" })
	second, err := cosmoMergeID(&rebuilt, args, target, sibling)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Errorf("cosmoMergeID = %x for both linkers; a changed linker under one stamped tool ID must not share a merge key", first)
	}

	base.SetSelf(filepath.Join(dir, "absent"), []string{"link"})
	var missing Builder
	missing.toolIDCache.Do("link", func() string { return "stamped-and-unchanged" })
	if _, err := cosmoMergeID(&missing, args, target, sibling); err == nil {
		t.Error("cosmoMergeID with an unreadable linker succeeded; a key that cannot name the linker must fail")
	}
}
