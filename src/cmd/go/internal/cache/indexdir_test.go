// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package cache

import (
	"os"
	"path/filepath"
	"testing"
)

// Two GOCACHE directories of one user must share one copy of the key index.
// make.bash runs under two, and each downloaded the whole index.
func TestIndexDirIsSharedAcrossCaches(t *testing.T) {
	userCache, err := os.UserCacheDir()
	if err != nil || userCache == "" {
		// wasip1 and js have no user cache directory. The copy then stays beside the cache.
		dir := t.TempDir()
		if got := indexDir(dir); got != dir {
			t.Fatalf("with no user cache directory, index directory = %s, want the cache's own %s", got, dir)
		}
		return
	}
	first, second := indexDir(t.TempDir()), indexDir(t.TempDir())
	if first != second {
		t.Fatalf("two caches got two index directories: %s and %s", first, second)
	}
	if want := filepath.Join(userCache, "go-build-index"); first != want {
		t.Fatalf("index directory = %s, want %s", first, want)
	}
	if info, err := os.Stat(first); err != nil || !info.IsDir() {
		t.Fatalf("index directory %s was not created: %v", first, err)
	}
}
