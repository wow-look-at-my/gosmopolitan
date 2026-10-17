// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package modfetch

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"cmd/go/internal/cache"

	"github.com/wow-look-at-my/go-s3-server/cacheclient"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

// zipCacheFixture is a module zip, its go.mod, and a fetcher whose go.sum
// records both of their sums.
type zipCacheFixture struct {
	dir     string
	store   cache.Cache
	fetcher *Fetcher
	mod     module.Version
	zipPath string
	zipSum  string
	goMod   []byte
}

func newZipCacheFixture(t *testing.T) *zipCacheFixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "cache"), 0o777); err != nil {
		t.Fatal(err)
	}
	store, err := cacheclient.Open(filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	mod := module.Version{Path: "example.com/zipcache", Version: "v1.0.0"}
	goMod := []byte("module example.com/zipcache\n")

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, body := range map[string][]byte{"go.mod": goMod, "lib.go": []byte("package zipcache\n")} {
		entry, err := writer.Create(mod.Path + "@" + mod.Version + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write(body)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(dir, "fetched.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o666); err != nil {
		t.Fatal(err)
	}
	zipSum, err := dirhash.HashZip(zipPath, dirhash.DefaultHash)
	if err != nil {
		t.Fatal(err)
	}
	modSum, err := goModSum(goMod)
	if err != nil {
		t.Fatal(err)
	}
	goSum := filepath.Join(dir, "go.sum")
	lines := mod.Path + " " + mod.Version + " " + zipSum + "\n" + mod.Path + " " + mod.Version + "/go.mod " + modSum + "\n"
	if err := os.WriteFile(goSum, []byte(lines), 0o666); err != nil {
		t.Fatal(err)
	}
	fetcher := NewFetcher()
	fetcher.SetGoSumFile(goSum)
	return &zipCacheFixture{dir: dir, store: store, fetcher: fetcher, mod: mod, zipPath: zipPath, zipSum: zipSum, goMod: goMod}
}

// TestZipFromCache: a zip that one build fetched comes from the cache for the
// next one, with the sum go.sum records, and with no request to its origin.
func TestZipFromCache(t *testing.T) {
	fx := newZipCacheFixture(t)
	ctx := context.Background()
	out := filepath.Join(fx.dir, "out", "v1.0.0.zip")
	if err := os.MkdirAll(filepath.Dir(out), 0o777); err != nil {
		t.Fatal(err)
	}

	if fx.fetcher.zipFromCache(ctx, fx.store, fx.mod, out, out+"hash") {
		t.Fatal("zipFromCache answered from an empty cache")
	}
	zipToCache(fx.store, fx.mod, fx.zipPath, fx.zipSum)
	if !fx.fetcher.zipFromCache(ctx, fx.store, fx.mod, out, out+"hash") {
		t.Fatal("zipFromCache missed a zip the cache holds")
	}
	want, _ := os.ReadFile(fx.zipPath)
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("zipFromCache wrote %d bytes, want the %d bytes stored (%v)", len(got), len(want), err)
	}
	hash, err := os.ReadFile(out + "hash")
	if err != nil || string(hash) != fx.zipSum {
		t.Fatalf("ziphash = %q (%v), want %q", hash, err, fx.zipSum)
	}
}

// TestZipFromCacheChecksTheSum: a body under the key that does not hash to
// the recorded sum is not used.
func TestZipFromCacheChecksTheSum(t *testing.T) {
	fx := newZipCacheFixture(t)
	other := filepath.Join(fx.dir, "other.zip")
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entry, _ := writer.Create(fx.mod.Path + "@" + fx.mod.Version + "/go.mod")
	entry.Write([]byte("module example.com/other\n"))
	writer.Close()
	if err := os.WriteFile(other, buf.Bytes(), 0o666); err != nil {
		t.Fatal(err)
	}
	cache.PutBytes(fx.store, zipCacheKey(fx.mod, fx.zipSum), buf.Bytes())

	out := filepath.Join(fx.dir, "v1.0.0.zip")
	if fx.fetcher.zipFromCache(context.Background(), fx.store, fx.mod, out, out+"hash") {
		t.Fatal("zipFromCache used a zip whose sum differs from go.sum's")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("zipFromCache left %s behind: %v", out, err)
	}
}

// TestGoModFromCache: a go.mod comes from the cache when its sum is the one
// go.sum records, and not otherwise.
func TestGoModFromCache(t *testing.T) {
	fx := newZipCacheFixture(t)
	ctx := context.Background()
	if _, ok := fx.fetcher.goModFromCache(ctx, fx.store, fx.mod.Path, fx.mod.Version, ""); ok {
		t.Fatal("goModFromCache answered from an empty cache")
	}
	goModToCache(fx.store, fx.mod.Path, fx.mod.Version, fx.goMod)
	got, ok := fx.fetcher.goModFromCache(ctx, fx.store, fx.mod.Path, fx.mod.Version, "")
	if !ok || !bytes.Equal(got, fx.goMod) {
		t.Fatalf("goModFromCache = %q, %v; want %q", got, ok, fx.goMod)
	}

	sum, _ := goModSum(fx.goMod)
	cache.PutBytes(fx.store, goModCacheKey(fx.mod.Path, "v1.0.1", sum), []byte("module example.com/other\n"))
	if _, ok := fx.fetcher.goModFromCache(ctx, fx.store, fx.mod.Path, "v1.0.1", ""); ok {
		t.Fatal("goModFromCache answered for a version go.sum does not record")
	}
}
