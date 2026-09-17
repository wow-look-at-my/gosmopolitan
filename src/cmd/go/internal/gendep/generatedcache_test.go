// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"os"
	"path/filepath"
	"testing"

	"cmd/go/internal/cache"
	"cmd/go/internal/cfg"
)

// TestGeneratedTreeRoundTripsThroughTheCache stores what a generator did and
// restores it onto a fresh copy of the module: a file it wrote, a file it
// changed and a file it removed all arrive, and a file it left alone is the
// module's own.
func TestGeneratedTreeRoundTripsThroughTheCache(test *testing.T) {
	test.Setenv("GOCACHE", test.TempDir())
	cfg.GOMODCACHE = test.TempDir()
	modroot := filepath.Join(test.TempDir(), "example.com", "gen@v1.0.0")
	writeFiles(test, modroot, map[string]string{
		"lang/lang.go":    "package lang\n",
		"lang/stale.go":   "package lang\n\n// removed by the generator\n",
		"lang/tables.txt": "old tables\n",
		"other/other.go":  "package other\n",
	})

	// A generator's stage: the module plus what it did.
	stage := filepath.Join(test.TempDir(), "stage")
	if err := copyTree(modroot, stage); err != nil {
		test.Fatal(err)
	}
	writeFiles(test, stage, map[string]string{
		"lang/lang.gen.go": "package lang\n\nfunc Name() string { return \"generated\" }\n",
		"lang/tables.txt":  "new tables\n",
	})
	if err := os.Remove(filepath.Join(stage, "lang", "stale.go")); err != nil {
		test.Fatal(err)
	}
	if err := storeGenerated(modroot, stage, "example.com/gen@v1.0.0", "lang"); err != nil {
		test.Fatal(err)
	}

	// The cache holds exactly the delta, keyed by module, package and host.
	if _, _, err := cache.GetBytes(cache.Default(), generatedKey("example.com/gen@v1.0.0", "lang")); err != nil {
		test.Fatalf("the delta is not in the cache: %v", err)
	}
	if _, _, err := cache.GetBytes(cache.Default(), generatedKey("example.com/gen@v1.0.0", "other")); err == nil {
		test.Fatal("a package nothing generated has a cache entry")
	}

	// A fresh module cache restores it without a generator.
	restored := filepath.Join(test.TempDir(), "restored")
	ok, err := restoreGenerated(modroot, restored, "example.com/gen@v1.0.0", "lang")
	if err != nil {
		test.Fatal(err)
	}
	if !ok {
		test.Fatal("the cache did not answer")
	}
	for rel, want := range map[string]string{
		"lang/lang.go":     "package lang\n",
		"lang/lang.gen.go": "package lang\n\nfunc Name() string { return \"generated\" }\n",
		"lang/tables.txt":  "new tables\n",
		"other/other.go":   "package other\n",
	} {
		got, err := os.ReadFile(filepath.Join(restored, rel))
		if err != nil {
			test.Errorf("%s: %v", rel, err)
			continue
		}
		if string(got) != want {
			test.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(restored, "lang", "stale.go")); !os.IsNotExist(err) {
		test.Errorf("lang/stale.go survived the restore: %v", err)
	}

	// A module version nothing generated is a miss, and leaves no stage.
	missed := filepath.Join(test.TempDir(), "missed")
	ok, err = restoreGenerated(modroot, missed, "example.com/gen@v2.0.0", "lang")
	if err != nil || ok {
		test.Fatalf("restoreGenerated of an unknown version = %v, %v; want false, nil", ok, err)
	}
	if _, err := os.Stat(missed); !os.IsNotExist(err) {
		test.Errorf("a miss left a stage behind: %v", err)
	}
}

// TestCachedGeneratedTreeStaysInsideTheModule refuses an archive member that
// names a path outside the stage.
func TestCachedGeneratedTreeStaysInsideTheModule(test *testing.T) {
	stage := filepath.Join(test.TempDir(), "stage")
	if err := os.MkdirAll(stage, 0o777); err != nil {
		test.Fatal(err)
	}
	for _, rel := range []string{"../escape.go", "."} {
		if _, err := stagePath(stage, rel); err == nil {
			test.Errorf("stagePath(%q) accepted a path outside the stage", rel)
		}
	}
	if _, err := stagePath(stage, "lang/lang.gen.go"); err != nil {
		test.Errorf("stagePath refused a path inside the stage: %v", err)
	}
}
