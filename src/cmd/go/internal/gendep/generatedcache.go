// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"bufio"
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

// The archive is a sequence of records, each a header line of the form
// "<kind> <size> <mode> <path>\n" followed by size bytes: kind "file" for a
// file the generator wrote or changed, "removed" (size 0) for one it removed.
// A path never holds a newline, so the line is unambiguous. archive/tar is
// not used because it reaches os/user, which the bootstrap go command may not.

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
	for _, rel := range wrote {
		path := filepath.Join(stage, rel)
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "file %d %o %s\n", info.Size(), info.Mode().Perm(), filepath.ToSlash(rel))
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		n, err := io.Copy(&buf, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		if n != info.Size() {
			return nil, fmt.Errorf("gendep: %s changed while being archived", path)
		}
	}
	for _, rel := range removed {
		fmt.Fprintf(&buf, "removed 0 0 %s\n", filepath.ToSlash(rel))
	}
	return buf.Bytes(), nil
}

// unpackGenerated applies an archive packGenerated wrote onto stage, a fresh
// copy of the fetched module. A record naming a path outside stage is refused.
func unpackGenerated(stage string, archive []byte) error {
	r := bufio.NewReader(bytes.NewReader(archive))
	for {
		line, err := r.ReadString('\n')
		if err == io.EOF && line == "" {
			return nil
		}
		if err != nil {
			return fmt.Errorf("gendep: cached generated tree is truncated")
		}
		var kind, mode string
		var size int64
		rest := strings.TrimSuffix(line, "\n")
		fields := strings.SplitN(rest, " ", 4)
		if len(fields) != 4 {
			return fmt.Errorf("gendep: cached generated tree has a malformed record %q", rest)
		}
		kind, mode = fields[0], fields[2]
		if _, err := fmt.Sscanf(fields[1], "%d", &size); err != nil || size < 0 {
			return fmt.Errorf("gendep: cached generated tree has a malformed record %q", rest)
		}
		dst, err := stagePath(stage, fields[3])
		if err != nil {
			return err
		}
		switch kind {
		case "removed":
			if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		case "file":
			var perm uint32
			if _, err := fmt.Sscanf(mode, "%o", &perm); err != nil {
				return fmt.Errorf("gendep: cached generated tree has a malformed record %q", rest)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
				return err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(perm)|0o200)
			if err != nil {
				return err
			}
			_, err = io.CopyN(f, r, size)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return fmt.Errorf("gendep: cached generated tree is truncated at %s", fields[3])
			}
		default:
			return fmt.Errorf("gendep: cached generated tree has a record of kind %q", kind)
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
	cacheDebugf("gendep: %s/%s: restored %d bytes from the build cache", modrel, pkgrel, len(archive))
	return true, nil
}

// storeGenerated puts what the generator did in stage into the cache.
func storeGenerated(modroot, stage, modrel, pkgrel string) error {
	archive, err := packGenerated(modroot, stage)
	if err != nil {
		return err
	}
	if err := cache.PutBytes(cache.Default(), generatedKey(modrel, pkgrel), archive); err != nil {
		return err
	}
	cacheDebugf("gendep: %s/%s: stored %d bytes in the build cache", modrel, pkgrel, len(archive))
	return nil
}

// cacheDebugf reports what the cache did for a generated tree, under the same
// variable that makes the build cache report itself. The name is spelled here
// because the shared tier that declares it is not part of go_bootstrap.
func cacheDebugf(format string, args ...any) {
	if os.Getenv("GOCACHEDEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "go: "+format+"\n", args...)
}
