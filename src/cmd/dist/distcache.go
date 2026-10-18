// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// dist compiles go_bootstrap itself, so no go command and no GOCACHE serves
// those packages. This cache keys each package archive and each command on
// every input that shapes its bytes, and lives under GOROOT/pkg/obj/go-build,
// the directory CI keeps between runs.

// distCacheVersion changes when the key loses an input it needs.
const distCacheVersion = "dist-cache-1"

// distCacheEnv lists the variables that change what the tools write.
var distCacheEnv = []string{
	"GOEXPERIMENT", "GOAMD64", "GOARM", "GOARM64", "GO386", "GOMIPS", "GOMIPS64",
	"GOPPC64", "GORISCV64", "GOWASM", "GOFIPS140", "GOROOT_FINAL", "GOOS", "GOARCH",
}

var fileHashes sync.Map // path, size and mtime -> hex digest

// fileDigest returns the SHA-256 of the file at path.
func fileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		fatalf("dist cache: %v", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// toolDigest is fileDigest for a large file that dist reads many times: a
// tool binary, or dist itself. A rewrite of the file changes its size or
// mtime, and that misses the memo.
func toolDigest(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		fatalf("dist cache: %v", err)
	}
	memo := fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
	if digest, ok := fileHashes.Load(memo); ok {
		return digest.(string)
	}
	digest := fileDigest(path)
	fileHashes.Store(memo, digest)
	return digest
}

// installKey is the cache key of one runInstall target: the tools, their
// flags, the sources, and the archives the package imports.
func installKey(pkg, pkgName string, ispackcmd bool, linkFlags, files, sfiles, sortedImports []string, importMap map[string]string) string {
	distCacheTrim()
	key := newDistKey()
	key.add("target", pkg, pkgName, fmt.Sprint(ispackcmd), goos, goarch, goarm, gomips, gomips64, goppc64, goriscv64, gogcflags, goldflags)
	key.add("compile", toolDigest(pathf("%s/compile%s", tooldir, exe)))
	if len(sfiles) > 0 {
		key.add("asm", toolDigest(pathf("%s/asm%s", tooldir, exe)))
		key.dir(pathf("%s/pkg/include", goroot))
	}
	if !ispackcmd {
		key.add("link", toolDigest(pathf("%s/link%s", tooldir, exe)))
		key.add(linkFlags...)
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for _, path := range sorted {
		key.file(path)
	}
	deps := make([]string, 0, len(sortedImports))
	for _, imp := range sortedImports {
		if imp == "unsafe" {
			continue
		}
		dep := importMap[imp]
		deps = append(deps, dep)
		if ispackcmd {
			// A compile reads only what its direct imports export.
			key.add("import", imp, dep, fileDigest(packagefile(dep)))
		} else {
			// A link reads every package below it, and a change deep down can leave each direct import's archive as it was.
			key.add("import", imp, dep, deepDigest(dep))
		}
	}
	packageImports.Store(pkg, deps)
	return key.sum()
}

var (
	packageImports sync.Map // package path -> the package paths it imports
	deepDigests    sync.Map // package path -> deepDigest
)

// deepDigest is the digest of the archive of pkg and of every archive below
// it. runInstall installs a package's imports before the package, so each
// has recorded its imports when this reads them.
func deepDigest(pkg string) string {
	if digest, ok := deepDigests.Load(pkg); ok {
		return digest.(string)
	}
	deps, ok := packageImports.Load(pkg)
	if !ok {
		// This run did not build pkg, so what is below it is not known. A digest no other run has makes the link a miss.
		return fmt.Sprintf("unknown %s %d", pkg, time.Now().UnixNano())
	}
	key := &distKey{h: sha256.New()}
	key.add(pkg, fileDigest(packagefile(pkg)))
	for _, dep := range deps.([]string) {
		key.add(dep, deepDigest(dep))
	}
	digest := key.sum()
	deepDigests.Store(pkg, digest)
	return digest
}

// distKey builds one cache key.
type distKey struct{ h hash.Hash }

func newDistKey() *distKey {
	key := &distKey{h: sha256.New()}
	key.add(distCacheVersion)
	key.add("dist", distSelfDigest())
	for _, name := range distCacheEnv {
		key.add(name, os.Getenv(name))
	}
	return key
}

// distSelfDigest keys dist by its sources, not by its binary. Under the APE
// loader os.Executable can find no path for dist, and the sources fix what dist does.
var distSelfDigest = sync.OnceValue(func() string {
	return dirDigest(pathf("%s/src/cmd/dist", goroot))
})

// dirDigest is the digest of every file of dir, by name and contents.
func dirDigest(dir string) string {
	key := &distKey{h: sha256.New()}
	key.dir(dir)
	return key.sum()
}

// add writes each part with its length, so no two lists of parts collide.
func (key *distKey) add(parts ...string) {
	for _, part := range parts {
		fmt.Fprintf(key.h, "%d:%s;", len(part), part)
	}
}

// file adds the name of a file under GOROOT and its contents.
func (key *distKey) file(path string) {
	rel, err := filepath.Rel(goroot, path)
	if err != nil {
		rel = path
	}
	key.add("file", filepath.ToSlash(rel), fileDigest(path))
}

// dir adds every file of a directory, by name and contents.
func (key *distKey) dir(dir string) {
	names := xreaddir(dir)
	sort.Strings(names)
	for _, name := range names {
		if path := filepath.Join(dir, name); isfile(path) {
			key.file(path)
		}
	}
}

func (key *distKey) sum() string {
	return hex.EncodeToString(key.h.Sum(nil))
}

func distCachePath(key string) string {
	return pathf("%s/pkg/obj/go-build/dist/%s/%s", goroot, key[:2], key)
}

// distCacheTrimAge is how long an entry no run used stays, as for GOCACHE.
const distCacheTrimAge = 5 * 24 * time.Hour

// distCacheGet copies the cached output for key to target, and reports
// whether there was one. A hit moves the entry's mtime to now, which is what
// distCacheTrim reads as use.
func distCacheGet(key, target string, flag int) bool {
	src := distCachePath(key)
	if !isfile(src) {
		return false
	}
	xmkdirall(filepath.Dir(target))
	copyfile(target, src, flag)
	now := time.Now()
	os.Chtimes(src, now, now)
	return true
}

var distCacheTrimOnce sync.Once

// distCacheTrim removes the entries no run used in distCacheTrimAge. CI keeps
// the directory between runs, so without this it grows with each change.
func distCacheTrim() {
	distCacheTrimOnce.Do(func() {
		root := pathf("%s/pkg/obj/go-build/dist", goroot)
		cutoff := time.Now().Add(-distCacheTrimAge)
		filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
				os.Remove(path)
			}
			return nil
		})
	})
}

// distCachePut stores target as the output for key. The rename makes the
// entry whole before a reader can see it.
func distCachePut(key, target string) {
	dst := distCachePath(key)
	xmkdirall(filepath.Dir(dst))
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		fatalf("dist cache: %v", err)
	}
	data, err := os.ReadFile(target)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
		fatalf("dist cache: store %s: %v", target, err)
	}
}
