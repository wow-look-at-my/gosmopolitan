// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package modfetch

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cmd/go/internal/cache"
	"cmd/go/internal/modfetch/codehost"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

// A module zip whose h1 sum is known comes from the build cache before any
// origin. The shared store then serves it to every machine, and an origin
// that is down stops no build that fetched the module once.

// zipCacheVersion names the entry format.
const zipCacheVersion = "modzip file v1"

// zipCacheKey is the cache key of the zip of mod whose h1 sum is sum.
func zipCacheKey(mod module.Version, sum string) cache.ActionID {
	h := cache.NewHash("modzipfile")
	fmt.Fprintf(h, "%s\n%s\n%s\n%s\n", zipCacheVersion, mod.Path, mod.Version, sum)
	return h.Sum()
}

// zipFromCache writes the zip of mod to zipfile from the build cache, when go.sum
// records an h1 sum for it and the cache holds a zip with that sum. It reports
// whether it wrote one. A nil cache, which GOCACHE=off gives, holds nothing.
func (f *Fetcher) zipFromCache(ctx context.Context, c cache.Cache, mod module.Version, zipfile, ziphashfile string) bool {
	if c == nil {
		return false
	}
	sum, ok := f.RecordedSum(mod)
	if !ok || !strings.HasPrefix(sum, "h1:") {
		return false
	}
	data, _, err := cache.GetBytes(c, zipCacheKey(mod, sum))
	if err != nil {
		return false
	}
	file, err := tempFile(ctx, filepath.Dir(zipfile), filepath.Base(zipfile), 0o666)
	if err != nil {
		return false
	}
	keep := false
	defer func() {
		if !keep {
			os.Remove(file.Name())
		}
	}()
	_, werr := file.Write(data)
	if err := file.Close(); werr != nil || err != nil {
		return false
	}
	// The key names the sum, and the sum is checked again: a body that does not
	// hash to it is not used.
	if err := zipPathsUnder(mod, data); err != nil {
		return false
	}
	got, err := dirhash.HashZip(file.Name(), dirhash.DefaultHash)
	if err != nil || got != sum {
		return false
	}
	if err := writeZiphash(ziphashfile, sum); err != nil {
		return false
	}
	if err := os.Rename(file.Name(), zipfile); err != nil {
		return false
	}
	keep = true
	codehost.FetchFrom(ctx).SetRoute("build cache")
	return true
}

// zipToCache stores the zip at zipfile, whose h1 sum is sum, for the next build.
func zipToCache(c cache.Cache, mod module.Version, zipfile, sum string) {
	if c == nil || !strings.HasPrefix(sum, "h1:") {
		return
	}
	data, err := os.ReadFile(zipfile)
	if err != nil {
		return
	}
	cache.PutBytes(c, zipCacheKey(mod, sum), data)
}

// goModCacheKey is the cache key of the go.mod of path@version whose h1 sum is sum.
func goModCacheKey(path, version, sum string) cache.ActionID {
	h := cache.NewHash("modgomod")
	fmt.Fprintf(h, "%s\n%s\n%s\n%s\n", zipCacheVersion, path, version, sum)
	return h.Sum()
}

// goModFromCache answers the go.mod of path@version from the build cache, when
// go.sum records its h1 sum and the cache holds a go.mod with that sum. It
// writes the go.mod to file, the module cache's copy.
func (f *Fetcher) goModFromCache(ctx context.Context, c cache.Cache, path, version, file string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	sum, ok := f.RecordedSum(module.Version{Path: path, Version: version + "/go.mod"})
	if !ok || !strings.HasPrefix(sum, "h1:") {
		return nil, false
	}
	data, _, err := cache.GetBytes(c, goModCacheKey(path, version, sum))
	if err != nil {
		return nil, false
	}
	if got, err := goModSum(data); err != nil || got != sum {
		return nil, false
	}
	if err := checkGoMod(f, path, version, "", data); err != nil {
		return nil, false
	}
	if file != "" {
		if err := writeDiskGoMod(ctx, file, data); err != nil && !isErrReadOnlyFS(err) {
			fmt.Fprintf(os.Stderr, "go: writing go.mod cache: %v\n", err)
		}
	}
	return data, true
}

// goModToCache stores the go.mod data of path@version for the next build.
func goModToCache(c cache.Cache, path, version string, data []byte) {
	if c == nil {
		return
	}
	sum, err := goModSum(data)
	if err != nil {
		return
	}
	cache.PutBytes(c, goModCacheKey(path, version, sum), data)
}

// zipPathsUnder reports an error when the zip in data holds a file outside the
// module's own directory.
func zipPathsUnder(mod module.Version, data []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	prefix := mod.Path + "@" + mod.Version + "/"
	for _, zf := range reader.File {
		if !strings.HasPrefix(zf.Name, prefix) {
			return fmt.Errorf("zip for %s has unexpected file %s", prefix[:len(prefix)-1], zf.Name)
		}
	}
	return nil
}
