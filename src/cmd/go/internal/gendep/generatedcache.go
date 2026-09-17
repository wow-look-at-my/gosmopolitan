// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"cmd/go/internal/cache"
	"cmd/go/internal/modfetch"
)

// What a generator wrote for a package of a module version is the same on
// every machine that runs it, so it is kept in the build cache like a compile
// output: one entry per (module version, package, host), holding the files the
// generator wrote or changed and the names of the ones it removed, relative to
// the fetched module. A build whose module cache has no generated tree yet asks
// the cache before it runs anything, and the shared tier answers from another
// machine's run.
//
// The host is part of the key because the generators run here, on this host,
// and the trees they leave are only ever compared within one host: every job
// that must agree byte for byte with another already generates on its own.

// removedListName is the archive member that lists removed files, one relative
// path per line. No file of a module carries the name: a module path never
// holds an at sign, which is what keeps it apart from the generated marks too.
const removedListName = "@removed"

// generatedKey is the cache key of the generated delta for package pkgrel of
// the module version at modrel (the module's path in the module cache).
func generatedKey(modrel, pkgrel string) cache.ActionID {
	h := cache.NewHash("gendep")
	fmt.Fprintf(h, "gendep v1\nmodule %s\npackage %s\nhost %s/%s\n", filepath.ToSlash(modrel), filepath.ToSlash(pkgrel), runtime.GOOS, runtime.GOARCH)
	return h.Sum()
}

// packGenerated archives what a generator did in stage relative to modroot.
func packGenerated(modroot, stage string) ([]byte, error) {
	wrote, removed, err := generatorChanges(modroot, stage)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, rel := range wrote {
		path := filepath.Join(stage, rel)
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     filepath.ToSlash(rel),
			Mode:     int64(info.Mode().Perm()),
			Size:     info.Size(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(removed) > 0 {
		var list strings.Builder
		for _, rel := range removed {
			list.WriteString(filepath.ToSlash(rel))
			list.WriteByte('\n')
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     removedListName,
			Mode:     0o644,
			Size:     int64(list.Len()),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := io.WriteString(tw, list.String()); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unpackGenerated applies an archive packGenerated wrote onto stage, a fresh
// copy of the fetched module. A member naming a path outside stage is refused.
func unpackGenerated(stage string, archive []byte) error {
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Name == removedListName {
			list, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			for _, rel := range strings.Split(strings.TrimSpace(string(list)), "\n") {
				if rel == "" {
					continue
				}
				dst, err := stagePath(stage, rel)
				if err != nil {
					return err
				}
				if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
			continue
		}
		dst, err := stagePath(stage, hdr.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(hdr.Mode)|0o200)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
}

// stagePath answers the path of rel inside stage, refusing one that escapes it.
func stagePath(stage, rel string) (string, error) {
	dst := filepath.Join(stage, filepath.FromSlash(rel))
	if dst == stage || !strings.HasPrefix(dst, stage+string(filepath.Separator)) {
		return "", fmt.Errorf("gendep: cached generated tree names %q outside the module", rel)
	}
	return dst, nil
}

// restoreGenerated stages the generated package from the cache: a copy of the
// fetched module with the cached delta applied. It reports whether the cache
// held one.
func restoreGenerated(modroot, stage, modrel, pkgrel string) (bool, error) {
	archive, _, err := cache.GetBytes(cache.Default(), generatedKey(modrel, pkgrel))
	if err != nil {
		return false, nil
	}
	if err := modfetch.RemoveAll(stage); err != nil {
		return false, err
	}
	if err := copyTree(modroot, stage); err != nil {
		modfetch.RemoveAll(stage)
		return false, err
	}
	if err := unpackGenerated(stage, archive); err != nil {
		modfetch.RemoveAll(stage)
		return false, err
	}
	return true, nil
}

// storeGenerated puts what the generator did in stage into the cache.
func storeGenerated(modroot, stage, modrel, pkgrel string) error {
	archive, err := packGenerated(modroot, stage)
	if err != nil {
		return err
	}
	return cache.PutBytes(cache.Default(), generatedKey(modrel, pkgrel), archive)
}
