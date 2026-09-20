// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"cmd/go/internal/cache"
	"cmd/go/internal/load"
)

// diagCache is a cache holding whatever a test puts in it, over a directory
// of output files named by their own hash, which is what cache.GetBytes
// reads and checks.
type diagCache struct {
	dir  string
	held map[cache.ActionID]cache.Entry
}

func newDiagCache(t *testing.T) *diagCache {
	return &diagCache{dir: t.TempDir(), held: make(map[cache.ActionID]cache.Entry)}
}

// hold stores data under key.
func (fake *diagCache) hold(t *testing.T, key cache.ActionID, data []byte) {
	out := cache.OutputID(sha256.Sum256(data))
	if err := os.WriteFile(fake.OutputFile(out), data, 0o666); err != nil {
		t.Fatal(err)
	}
	fake.held[key] = cache.Entry{OutputID: out, Size: int64(len(data))}
}

func (fake *diagCache) Get(key cache.ActionID) (cache.Entry, error) {
	entry, ok := fake.held[key]
	if !ok {
		return cache.Entry{}, errors.New("cache: miss")
	}
	return entry, nil
}

func (fake *diagCache) Put(cache.ActionID, io.ReadSeeker) (cache.OutputID, int64, error) {
	return cache.OutputID{}, 0, errors.New("cache: this test puts nothing")
}

func (fake *diagCache) Close() error { return nil }

func (fake *diagCache) OutputFile(out cache.OutputID) string {
	return filepath.Join(fake.dir, fmt.Sprintf("%x", out))
}

func (fake *diagCache) FuzzDir() string { return fake.dir }

// TestCachedDiagnostics pins which cached results carry the tool output a
// build replays, and which leave the build to run the tool again.
func TestCachedDiagnostics(t *testing.T) {
	compile := cache.ActionID{1}
	main := &Action{Mode: "build", actionID: compile, Package: &load.Package{PackagePublic: load.PackagePublic{Name: "main"}}}
	link := &Action{Mode: "link", actionID: cache.ActionID{2}, Deps: []*Action{main}}
	other := &Action{Mode: "preprocess PGO profile", actionID: cache.ActionID{3}}

	tests := []struct {
		name string
		act  *Action
		key  string
		want bool
	}{
		{"compile with its output", main, "stdout", true},
		{"compile without it", main, "", false},
		{"link with the main package's output", link, "link-stdout", true},
		{"link without it", link, "", false},
		{"a mode that stores none", other, "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := newDiagCache(t)
			if test.key != "" {
				fake.hold(t, cache.Subkey(compile, test.key), []byte("dumped\n"))
			}
			if got := cachedDiagnostics(fake, test.act); got != test.want {
				t.Errorf("cachedDiagnostics = %v, want %v", got, test.want)
			}
		})
	}
}
