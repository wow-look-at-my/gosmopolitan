// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modfetch

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/fsys"
	"cmd/go/internal/gover"
	"cmd/go/internal/lockedfile"
	"cmd/go/internal/modfetch/codehost"
	"cmd/go/internal/orgmod"
	"cmd/go/internal/str"
	"cmd/go/internal/trace"
	"cmd/internal/par"
	"cmd/internal/robustio"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
)

var ErrToolchain = errors.New("internal error: invalid operation on toolchain module")

// Download downloads the specific module version to the
// local download cache and returns the name of the directory
// corresponding to the root of the module's file tree.
func (f *Fetcher) Download(ctx context.Context, mod module.Version) (dir string, err error) {
	if gover.IsToolchain(mod.Path) {
		return "", ErrToolchain
	}
	if err := checkCacheDir(ctx); err != nil {
		base.Fatal(err)
	}

	// The par.Cache here avoids duplicate work.
	return f.downloadCache.Do(mod, func() (string, error) {
		dir, err := f.download(ctx, mod)
		if err != nil {
			return "", err
		}
		f.checkMod(ctx, mod)

		// If go.mod exists (not an old legacy module), check version is not too new.
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			goVersion := gover.GoModLookup(data, "go")
			if gover.Compare(goVersion, gover.Local()) > 0 {
				return "", &gover.TooNewError{What: mod.String(), GoVersion: goVersion}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}

		return dir, nil
	})
}

// Unzip is like Download but is given the explicit zip file to use,
// rather than downloading it. This is used for the GOFIPS140 zip files,
// which ship in the Go distribution itself.
func (f *Fetcher) Unzip(ctx context.Context, mod module.Version, zipfile string) (dir string, err error) {
	if err := checkCacheDir(ctx); err != nil {
		base.Fatal(err)
	}

	return f.downloadCache.Do(mod, func() (string, error) {
		ctx, span := trace.StartSpan(ctx, "modfetch.Unzip "+mod.String())
		defer span.Done()

		dir, err = DownloadDir(ctx, mod)
		if err == nil {
			// The directory has already been completely extracted (no .partial file exists).
			return dir, nil
		} else if dir == "" || !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}

		return unzip(ctx, mod, zipfile, nil)
	})
}

func (f *Fetcher) download(ctx context.Context, mod module.Version) (dir string, err error) {
	ctx, span := trace.StartSpan(ctx, "modfetch.download "+mod.String())
	defer span.Done()

	dir, err = DownloadDir(ctx, mod)
	if err == nil {
		// The directory has already been completely extracted (no .partial file exists).
		return dir, nil
	} else if dir == "" || !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	// To avoid cluttering the cache with extraneous files,
	// DownloadZip uses the same lockfile as Download.
	// Invoke DownloadZip before locking the file.
	zipfile, err := f.DownloadZip(ctx, mod)
	if err != nil {
		return "", err
	}
	if zipfile == "" {
		// DownloadZip already wrote the source files to dir.
		return DownloadDir(ctx, mod)
	}

	// A module is two zips. The BASE zip is the one above: what the proxy
	// served, pinned by go.sum. The OVERLAY zip holds the files the module's
	// own generators add to it, and it is the one the cache server keeps. See
	// overlay.go.
	return unzip(ctx, mod, zipfile, func(dir string) error {
		return f.completeDir(ctx, mod, dir)
	})
}

// unzip extracts zipfile as mod's directory. complete, when given, runs over
// the extracted tree before the directory is published, and what it adds is
// part of the module from then on.
func unzip(ctx context.Context, mod module.Version, zipfile string, complete func(dir string) error) (dir string, err error) {
	unlock, err := lockVersion(ctx, mod)
	if err != nil {
		return "", err
	}
	defer unlock()

	ctx, span := trace.StartSpan(ctx, "unzip "+zipfile)
	defer span.Done()

	return populateDir(ctx, mod, func(dir string) error {
		return modzip.Unzip(dir, mod, zipfile)
	}, complete)
}

// populateDir makes mod's directory with write, then runs complete over it.
// The caller holds the version lock.
func populateDir(ctx context.Context, mod module.Version, write func(dir string) error, complete func(dir string) error) (dir string, err error) {
	// Check whether the directory was populated while we were waiting on the lock.
	dir, dirErr := DownloadDir(ctx, mod)
	if dirErr == nil {
		return dir, nil
	}
	_, dirExists := dirErr.(*DownloadDirPartialError)

	// Clean up any remaining temporary directories created by old versions
	// (before 1.16), as well as partially extracted directories (indicated by
	// DownloadDirPartialError, usually because of a .partial file). This is only
	// safe to do because the lock file ensures that their writers are no longer
	// active.
	parentDir := filepath.Dir(dir)
	tmpPrefix := filepath.Base(dir) + ".tmp-"
	if old, err := filepath.Glob(filepath.Join(str.QuoteGlob(parentDir), str.QuoteGlob(tmpPrefix)+"*")); err == nil {
		for _, path := range old {
			RemoveAll(path) // best effort
		}
	}
	if dirExists {
		if err := RemoveAll(dir); err != nil {
			return "", err
		}
	}

	partialPath, err := CachePath(ctx, mod, "partial")
	if err != nil {
		return "", err
	}

	// Extract the module zip directory at its final location.
	//
	// To prevent other processes from reading the directory if we crash,
	// create a .partial file before extracting the directory, and delete
	// the .partial file afterward (all while holding the lock).
	//
	// Before Go 1.16, we extracted to a temporary directory with a random name
	// then renamed it into place with os.Rename. On Windows, this failed with
	// ERROR_ACCESS_DENIED when another process (usually an anti-virus scanner)
	// opened files in the temporary directory.
	//
	// Go 1.14.2 and higher respect .partial files. Older versions may use
	// partially extracted directories. 'go mod verify' can detect this,
	// and 'go clean -modcache' can fix it.
	if err := os.MkdirAll(parentDir, 0o777); err != nil {
		return "", err
	}
	if err := os.WriteFile(partialPath, nil, 0o666); err != nil {
		return "", err
	}
	if err := write(dir); err != nil {
		fmt.Fprintf(os.Stderr, "-> %s\n", err)
		if rmErr := RemoveAll(dir); rmErr == nil {
			os.Remove(partialPath)
		}
		return "", err
	}
	// The module is completed while it is still marked partial, so no other
	// process reads a directory that has the base files and not the generated
	// ones.
	if complete != nil {
		if err := complete(dir); err != nil {
			if rmErr := RemoveAll(dir); rmErr == nil {
				os.Remove(partialPath)
			}
			return "", err
		}
	}
	if err := os.Remove(partialPath); err != nil {
		return "", err
	}

	if !cfg.ModCacheRW {
		makeDirsReadOnly(dir)
	}
	return dir, nil
}

var downloadZipCache par.ErrCache[module.Version, string]

// DownloadZip downloads the specific module version to the
// local zip cache and returns the name of the zip file.
// A module read from a GitHub archive has no zip. For it, DownloadZip writes
// the source files to $GOMODCACHE/<module>@<version> and returns "".
func (f *Fetcher) DownloadZip(ctx context.Context, mod module.Version) (zipfile string, err error) {
	// The par.Cache here avoids duplicate work.
	return downloadZipCache.Do(mod, func() (string, error) {
		zipfile, err := CachePath(ctx, mod, "zip")
		if err != nil {
			return "", err
		}
		ziphashfile := zipfile + "hash"

		// Return early if the ziphash exists with the zip or with a
		// complete directory.
		if _, err := os.Stat(ziphashfile); err == nil {
			have := zipfile
			if _, err := os.Stat(zipfile); err != nil {
				have = ""
			}
			if _, dirErr := DownloadDir(ctx, mod); have != "" || dirErr == nil {
				if !HaveSum(f, mod) {
					f.checkMod(ctx, mod)
				}
				return have, nil
			}
		}

		// The zip or ziphash file does not exist. Acquire the lock and create them.
		if cfg.CmdName != "mod download" {
			vers := mod.Version
			if mod.Path == "golang.org/toolchain" {
				// Shorten v0.0.1-go1.13.1.darwin-amd64 to go1.13.1.darwin-amd64
				_, vers, _ = strings.Cut(vers, "-")
				if i := strings.LastIndex(vers, "."); i >= 0 {
					goos, goarch, _ := strings.Cut(vers[i+1:], "-")
					vers = vers[:i] + " (" + goos + "/" + goarch + ")"
				}
				fmt.Fprintf(os.Stderr, "go: downloading %s\n", vers)
			} else {
				fmt.Fprintf(os.Stderr, "go: downloading %s %s\n", mod.Path, vers)
			}
		}
		unlock, err := lockVersion(ctx, mod)
		if err != nil {
			return "", err
		}
		defer unlock()

		return f.downloadZip(ctx, mod, zipfile)
	})
}

// downloadZip returns zipfile. For a module read from a GitHub archive, it
// writes the source files to $GOMODCACHE/<module>@<version> itself, makes no
// zip, and returns "".
func (f *Fetcher) downloadZip(ctx context.Context, mod module.Version, zipfile string) (_ string, err error) {
	ctx, span := trace.StartSpan(ctx, "modfetch.downloadZip "+zipfile)
	defer span.Done()

	// Double-check that the zipfile was not created while we were waiting for
	// the lock in DownloadZip.
	ziphashfile := zipfile + "hash"
	var zipExists, ziphashExists bool
	if _, err := os.Stat(zipfile); err == nil {
		zipExists = true
	}
	if _, err := os.Stat(ziphashfile); err == nil {
		ziphashExists = true
	}
	if zipExists && ziphashExists {
		return zipfile, nil
	}
	if _, dirErr := DownloadDir(ctx, mod); ziphashExists && dirErr == nil {
		return "", nil
	}

	// Create parent directories.
	if err := os.MkdirAll(filepath.Dir(zipfile), 0o777); err != nil {
		return "", err
	}

	// Clean up any remaining tempfiles from previous runs.
	// This is only safe to do because the lock file ensures that their
	// writers are no longer active.
	tmpPattern := filepath.Base(zipfile) + "*.tmp"
	if old, err := filepath.Glob(filepath.Join(str.QuoteGlob(filepath.Dir(zipfile)), tmpPattern)); err == nil {
		for _, path := range old {
			os.Remove(path) // best effort
		}
	}

	// If the zip file exists, the ziphash file must have been deleted
	// or lost after a file system crash. Re-hash the zip without downloading.
	if zipExists {
		return zipfile, hashZip(f, mod, zipfile, ziphashfile)
	}

	// From here to the os.Rename call below is functionally almost equivalent to
	// renameio.WriteToFile, with one key difference: we want to validate the
	// contents of the file (by hashing it) before we commit it. Because the file
	// is zip-compressed, we need an actual file — or at least an io.ReaderAt — to
	// validate it: we can't just tee the stream as we write it.
	// The file is made only when a source serves a zip.
	var file *os.File
	defer func() {
		if err != nil && file != nil {
			file.Close()
			os.Remove(file.Name())
		}
	}()

	var files []modzip.File
	var commit string
	fromFiles := false
	var zipRepo Repo
	var unrecoverableErr error
	err = TryProxies(func(proxy string) error {
		if unrecoverableErr != nil {
			return unrecoverableErr
		}
		repo := f.Lookup(ctx, proxy, mod.Path)
		if direct, ok := repo.(filesRepo); ok {
			got, gotCommit, err := direct.Files(ctx, mod.Version)
			if !errors.Is(err, errors.ErrUnsupported) {
				files, commit, fromFiles = got, gotCommit, err == nil
				return err
			}
		}
		zipRepo = repo
		if file == nil {
			var err error
			file, err = tempFile(ctx, filepath.Dir(zipfile), filepath.Base(zipfile), 0o666)
			if err != nil {
				unrecoverableErr = err
				return err
			}
		}
		err := repo.Zip(ctx, file, mod.Version)
		if err != nil {
			// Zip may have partially written to f before failing.
			// (Perhaps the server crashed while sending the file?)
			// Since we allow fallback on error in some cases, we need to fix up the
			// file to be empty again for the next attempt.
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				unrecoverableErr = err
				return err
			}
			if err := file.Truncate(0); err != nil {
				unrecoverableErr = err
				return err
			}
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if fromFiles {
		if file != nil {
			file.Close()
			os.Remove(file.Name())
		}
		return "", f.writeFiles(ctx, mod, files, commit, ziphashfile)
	}

	// Double-check that the paths within the zip file are well-formed.
	//
	// TODO(bcmills): There is a similar check within the Unzip function. Can we eliminate one?
	fi, err := file.Stat()
	if err != nil {
		return "", err
	}
	z, err := zip.NewReader(file, fi.Size())
	if err != nil {
		return "", err
	}
	prefix := mod.Path + "@" + mod.Version + "/"
	for _, zf := range z.File {
		if !strings.HasPrefix(zf.Name, prefix) {
			return "", fmt.Errorf("zip for %s has unexpected file %s", prefix[:len(prefix)-1], zf.Name)
		}
	}

	if err := file.Close(); err != nil {
		return "", err
	}

	// Check the sum before renaming to the final location. A zip that git made
	// from a github.com commit is identified by the commit and is not hashed.
	sum, err := f.commitSum(mod, githubCommit(ctx, zipRepo, mod.Version), func() (string, error) {
		return dirhash.HashZip(file.Name(), dirhash.DefaultHash)
	})
	if err != nil {
		return "", err
	}
	if err := checkModSum(f, mod, sum); err != nil {
		return "", err
	}
	if err := writeZiphash(ziphashfile, sum); err != nil {
		return "", err
	}
	if err := os.Rename(file.Name(), zipfile); err != nil {
		return "", err
	}

	// TODO(bcmills): Should we make the .zip and .ziphash files read-only to discourage tampering?

	return zipfile, nil
}

// writeFiles writes the source files of mod to $GOMODCACHE/<module>@<version>,
// as Unzip writes a zip's entries, and records their hash in ziphashfile.
// No zip is made. The caller holds the version lock.
func (f *Fetcher) writeFiles(ctx context.Context, mod module.Version, files []modzip.File, commit, ziphashfile string) error {
	valid, err := checkModuleFiles(mod, files)
	if err != nil {
		return err
	}
	hash, err := f.commitSum(mod, commit, func() (string, error) { return moduleFilesSum(mod, valid) })
	if err != nil {
		return err
	}
	if err := checkModSum(f, mod, hash); err != nil {
		return err
	}

	_, err = populateDir(ctx, mod, func(dir string) error {
		return writeModuleFiles(dir, valid)
	}, func(dir string) error {
		return f.completeDir(ctx, mod, dir)
	})
	if err != nil {
		return err
	}
	return writeZiphash(ziphashfile, hash)
}

// checkModuleFiles applies the checks a module zip gets to files. It returns
// the files that belong in the module, in order.
func checkModuleFiles(mod module.Version, files []modzip.File) ([]modzip.File, error) {
	if err := module.Check(mod.Path, mod.Version); err != nil {
		return nil, err
	}
	checked, err := modzip.CheckFiles(files)
	if err != nil {
		return nil, err
	}
	var valid []modzip.File
	for _, file := range files {
		if slices.Contains(checked.Valid, file.Path()) {
			valid = append(valid, file)
		}
	}
	return valid, nil
}

// moduleFilesSum answers the h1 sum of files, the same sum HashZip gives for a
// module zip of them. It hashes the files where they are. It makes no zip.
func moduleFilesSum(mod module.Version, files []modzip.File) (string, error) {
	prefix := mod.Path + "@" + mod.Version + "/"
	byName := make(map[string]modzip.File, len(files))
	names := make([]string, 0, len(files))
	for _, file := range files {
		byName[prefix+file.Path()] = file
		names = append(names, prefix+file.Path())
	}
	return dirhash.DefaultHash(names, func(name string) (io.ReadCloser, error) {
		return byName[name].Open()
	})
}

// A githubCommitRepo serves the versions of a github.com repository. The
// commit of a version identifies its files.
type githubCommitRepo interface {
	GitHubCommit(ctx context.Context, version string) (string, error)
}

// githubCommit answers the commit repo holds version at, or "" when repo is not
// a github.com repository.
func githubCommit(ctx context.Context, repo Repo, version string) string {
	direct, ok := repo.(githubCommitRepo)
	if !ok {
		return ""
	}
	commit, err := direct.GitHubCommit(ctx, version)
	if err != nil || len(commit) != 40 || !codehost.AllHex(commit) {
		return ""
	}
	return commit
}

// commitSum answers the sum to record for mod when commit identifies it: the
// git sum, with nothing hashed. A go.sum that records an h1 sum for mod gets
// the h1 sum from hash, so it can be checked.
func (f *Fetcher) commitSum(mod module.Version, commit string, hash func() (string, error)) (string, error) {
	if commit == "" || f.recordsH1(mod) {
		return hash()
	}
	return gitSumPrefix + commit, nil
}

// writeModuleFiles writes each file under dir, read-only, as Unzip does.
func writeModuleFiles(dir string, files []modzip.File) error {
	if entries, _ := os.ReadDir(dir); len(entries) > 0 {
		return fmt.Errorf("target directory %v exists and is not empty", dir)
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	for _, file := range files {
		dst := filepath.Join(dir, filepath.FromSlash(file.Path()))
		if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
		if err != nil {
			src.Close()
			return err
		}
		_, err = io.Copy(out, src)
		src.Close()
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// hashZip reads the zip file opened in f, then writes the hash to ziphashfile,
// overwriting that file if it exists.
//
// If the hash does not match go.sum (or the sumdb if enabled), hashZip returns
// an error and does not write ziphashfile.
func hashZip(f *Fetcher, mod module.Version, zipfile, ziphashfile string) (err error) {
	hash, err := dirhash.HashZip(zipfile, dirhash.DefaultHash)
	if err != nil {
		return err
	}
	if err := checkModSum(f, mod, hash); err != nil {
		return err
	}
	return writeZiphash(ziphashfile, hash)
}

// writeZiphash records hash in ziphashfile, overwriting that file if it exists.
func writeZiphash(ziphashfile, hash string) (err error) {
	hf, err := lockedfile.Create(ziphashfile)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := hf.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if err := hf.Truncate(int64(len(hash))); err != nil {
		return err
	}
	if _, err := hf.WriteAt([]byte(hash), 0); err != nil {
		return err
	}
	return nil
}

// makeDirsReadOnly makes a best-effort attempt to remove write permissions for dir
// and its transitive contents.
func makeDirsReadOnly(dir string) {
	type pathMode struct {
		path string
		mode fs.FileMode
	}
	var dirs []pathMode // in lexical order
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			info, err := d.Info()
			if err == nil && info.Mode()&0o222 != 0 {
				dirs = append(dirs, pathMode{path, info.Mode()})
			}
		}
		return nil
	})

	// Run over list backward to chmod children before parents.
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Chmod(dirs[i].path, dirs[i].mode&^0o222)
	}
}

// RemoveAll removes a directory written by Download or Unzip, first applying
// any permission changes needed to do so.
func RemoveAll(dir string) error {
	// Module cache has 0555 directories; make them writable in order to remove content.
	filepath.WalkDir(dir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return nil // ignore errors walking in file system
		}
		if info.IsDir() {
			os.Chmod(path, 0o777)
		}
		return nil
	})
	return robustio.RemoveAll(dir)
}

// The GoSumFile, WorkspaceGoSumFiles, and goSum are global state that must not be
// accessed by any of the exported functions of this package after they return, because
// they can be modified by the non-thread-safe SetState function.

type modSum struct {
	mod module.Version
	sum string
}

type sumState struct {
	m         map[module.Version][]string            // content of go.sum file
	w         map[string]map[module.Version][]string // sum file in workspace -> content of that sum file
	status    map[modSum]modSumStatus                // state of sums in m
	overwrite bool                                   // if true, overwrite go.sum without incorporating its contents
	enabled   bool                                   // whether to use go.sum at all
}

type modSumStatus struct {
	used, dirty bool
}

// Fetcher holds a snapshot of the global state of the modfetch package.
type Fetcher struct {
	// path to go.sum; set by package modload
	goSumFile string
	// path to module go.sums in workspace; set by package modload
	workspaceGoSumFiles []string
	// The Lookup cache is used cache the work done by Lookup.
	// It is important that the global functions of this package that access it do not
	// do so after they return.
	lookupCache *par.Cache[lookupCacheKey, Repo]
	// The downloadCache is used to cache the operation of downloading a module to disk
	// (if it's not already downloaded) and getting the directory it was downloaded to.
	// It is important that downloadCache must not be accessed by any of the exported
	// functions of this package after they return, because it can be modified by the
	// non-thread-safe SetState function.
	downloadCache *par.ErrCache[module.Version, string] // version → directory;

	mu       sync.Mutex
	sumState sumState
}

func NewFetcher() *Fetcher {
	f := new(Fetcher)
	f.lookupCache = new(par.Cache[lookupCacheKey, Repo])
	f.downloadCache = new(par.ErrCache[module.Version, string])
	return f
}

func (f *Fetcher) GoSumFile() string {
	return f.goSumFile
}

func (f *Fetcher) SetGoSumFile(str string) {
	f.goSumFile = str
}

func (f *Fetcher) AddWorkspaceGoSumFile(file string) {
	f.workspaceGoSumFiles = append(f.workspaceGoSumFiles, file)
}

// Reset resets globals in the modfetch package, so previous loads don't affect
// contents of go.sum files.
func (f *Fetcher) Reset() {
	f.SetState(NewFetcher())
}

// SetState sets the global state of the modfetch package to the newState, and returns the previous
// global state. newState should have been returned by SetState, or be an empty State.
// There should be no concurrent calls to any of the exported functions of this package with
// a call to SetState because it will modify the global state in a non-thread-safe way.
func (f *Fetcher) SetState(newState *Fetcher) (oldState *Fetcher) {
	if newState.lookupCache == nil {
		newState.lookupCache = new(par.Cache[lookupCacheKey, Repo])
	}
	if newState.downloadCache == nil {
		newState.downloadCache = new(par.ErrCache[module.Version, string])
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	oldState = &Fetcher{
		goSumFile:           f.goSumFile,
		workspaceGoSumFiles: f.workspaceGoSumFiles,
		lookupCache:         f.lookupCache,
		downloadCache:       f.downloadCache,
		sumState:            f.sumState,
	}

	f.SetGoSumFile(newState.goSumFile)
	f.workspaceGoSumFiles = newState.workspaceGoSumFiles
	// Uses of lookupCache and downloadCache both can call checkModSum,
	// which in turn sets the used bit on goSum.status for modules.
	// Set (or reset) them so used can be computed properly.
	f.lookupCache = newState.lookupCache
	f.downloadCache = newState.downloadCache
	// Set, or reset all fields on goSum. If being reset to empty, it will be initialized later.
	f.sumState = newState.sumState

	return oldState
}

// initGoSum initializes the go.sum data.
// The boolean it returns reports whether the
// use of go.sum is now enabled.
// The goSum lock must be held.
func (f *Fetcher) initGoSum() (bool, error) {
	if f.goSumFile == "" {
		return false, nil
	}
	if f.sumState.m != nil {
		return true, nil
	}

	f.sumState.m = make(map[module.Version][]string)
	f.sumState.status = make(map[modSum]modSumStatus)
	f.sumState.w = make(map[string]map[module.Version][]string)

	for _, fn := range f.workspaceGoSumFiles {
		f.sumState.w[fn] = make(map[module.Version][]string)
		_, err := readGoSumFile(f.sumState.w[fn], fn)
		if err != nil {
			return false, err
		}
	}

	enabled, err := readGoSumFile(f.sumState.m, f.goSumFile)
	f.sumState.enabled = enabled
	return enabled, err
}

func readGoSumFile(dst map[module.Version][]string, file string) (bool, error) {
	var (
		data []byte
		err  error
	)
	if fsys.Replaced(file) {
		// Don't lock go.sum if it's part of the overlay.
		// On Plan 9, locking requires chmod, and we don't want to modify any file
		// in the overlay. See #44700.
		data, err = os.ReadFile(fsys.Actual(file))
	} else {
		data, err = lockedfile.Read(file)
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	readGoSum(dst, file, data)

	return true, nil
}

// emptyGoModHash is the hash of a 1-file tree containing a 0-length go.mod.
// A bug caused us to write these into go.sum files for non-modules.
// We detect and remove them.
const emptyGoModHash = "h1:G7mAYYxgmS0lVkHyy2hEOLQCFB0DlQFTMLWggykrydY="

// readGoSum parses data, which is the content of file,
// and adds it to goSum.m. The goSum lock must be held.
func readGoSum(dst map[module.Version][]string, file string, data []byte) {
	lineno := 0
	for len(data) > 0 {
		var line []byte
		lineno++
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			line, data = data, nil
		} else {
			line, data = data[:i], data[i+1:]
		}
		f := strings.Fields(string(line))
		if len(f) == 0 {
			// blank line; skip it
			continue
		}
		if len(f) != 3 {
			if cfg.CmdName == "mod tidy" {
				// ignore malformed line so that go mod tidy can fix go.sum
				continue
			} else {
				base.Fatalf("malformed go.sum:\n%s:%d: wrong number of fields %v\n", file, lineno, len(f))
			}
		}
		if f[2] == emptyGoModHash {
			// Old bug; drop it.
			continue
		}
		mod := module.Version{Path: f[0], Version: f[1]}
		dst[mod] = append(dst[mod], f[2])
	}
}

// HaveSum returns true if the go.sum file contains an entry for mod.
// The entry's hash must be generated with a known hash algorithm.
// mod.Version may have a "/go.mod" suffix to distinguish sums for
// .mod and .zip files.
//
// An org module has no sum: cmd/go neither reads nor writes go.sum for one,
// and the git commit the module resolves to is the integrity check. HaveSum
// reports true for such a module so that no caller concludes its sum is
// missing, and so that a stale line left in go.sum is never consulted.
func HaveSum(f *Fetcher, mod module.Version) bool {
	if orgmod.IsOrg(mod.Path) {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	inited, err := f.initGoSum()
	if err != nil || !inited {
		return false
	}
	for _, goSums := range f.sumState.w {
		for _, h := range goSums[mod] {
			if !knownSum(h) {
				continue
			}
			if !f.sumState.status[modSum{mod, h}].dirty {
				return true
			}
		}
	}
	for _, h := range f.sumState.m[mod] {
		if !knownSum(h) {
			continue
		}
		if !f.sumState.status[modSum{mod, h}].dirty {
			return true
		}
	}
	return false
}

// RecordedSum returns the sum if the go.sum file contains an entry for mod.
// The boolean reports true if an entry was found or
// false if no entry found or two conflicting sums are found.
// The entry's hash must be generated with a known hash algorithm.
// mod.Version may have a "/go.mod" suffix to distinguish sums for
// .mod and .zip files.
//
// An org module has no sum, so RecordedSum always reports false for one.
func (f *Fetcher) RecordedSum(mod module.Version) (sum string, ok bool) {
	if orgmod.IsOrg(mod.Path) {
		return "", false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	inited, err := f.initGoSum()
	if err != nil || !inited {
		return "", false
	}
	// An h1 sum wins over a git sum, because every go command can check it.
	foundH1, foundGit := "", ""
	consider := func(h string) bool {
		if !knownSum(h) || f.sumState.status[modSum{mod, h}].dirty {
			return true
		}
		found := &foundH1
		if IsGitSum(h) {
			found = &foundGit
		}
		if *found != "" && *found != h { // conflicting sums exist
			return false
		}
		*found = h
		return true
	}
	for _, goSums := range f.sumState.w {
		for _, h := range goSums[mod] {
			if !consider(h) {
				return "", false
			}
		}
	}
	for _, h := range f.sumState.m[mod] {
		if !consider(h) {
			return "", false
		}
	}
	if foundH1 != "" {
		return foundH1, true
	}
	return foundGit, true
}

// checkMod checks the given module's checksum and Go version.
func (f *Fetcher) checkMod(ctx context.Context, mod module.Version) {
	// Do the file I/O before acquiring the go.sum lock.
	ziphash, err := CachePath(ctx, mod, "ziphash")
	if err != nil {
		base.Fatalf("verifying %v", module.VersionError(mod, err))
	}
	data, err := lockedfile.Read(ziphash)
	if err != nil {
		base.Fatalf("verifying %v", module.VersionError(mod, err))
	}
	data = bytes.TrimSpace(data)
	if !isValidSum(data) {
		// Recreate ziphash file from zip file and use that to check the mod sum.
		zip, err := CachePath(ctx, mod, "zip")
		if err != nil {
			base.Fatalf("verifying %v", module.VersionError(mod, err))
		}
		err = hashZip(f, mod, zip, ziphash)
		if err != nil {
			base.Fatalf("verifying %v", module.VersionError(mod, err))
		}
		return
	}
	h := string(data)
	if !knownSum(h) {
		base.Fatalf("verifying %v", module.VersionError(mod, fmt.Errorf("unexpected ziphash: %q", h)))
	}

	if err := checkModSum(f, mod, h); err != nil {
		base.Fatalf("%s", err)
	}
}

// goModSum returns the checksum for the go.mod contents.
func goModSum(data []byte) (string, error) {
	return dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
}

// checkGoMod checks the given module's go.mod checksum;
// data is the go.mod content. commit, when set, is the github.com commit the
// go.mod came from, which identifies it without a hash.
func checkGoMod(f *Fetcher, path, version, commit string, data []byte) error {
	mod := module.Version{Path: path, Version: version + "/go.mod"}
	h, err := f.commitSum(mod, commit, func() (string, error) { return goModSum(data) })
	if err != nil {
		return &module.ModuleError{Path: path, Version: version, Err: fmt.Errorf("verifying go.mod: %v", err)}
	}

	return checkModSum(f, mod, h)
}

// checkModSum checks that the recorded checksum for mod is h.
//
// mod.Version may have the additional suffix "/go.mod" to request the checksum
// for the module's go.mod file only.
//
// An org module has no checksum to verify or record: its commit is the check.
func checkModSum(f *Fetcher, mod module.Version, h string) error {
	if orgmod.IsOrg(mod.Path) {
		return nil
	}

	// We lock goSum when manipulating it,
	// but we arrange to release the lock when calling checkSumDB,
	// so that parallel calls to checkModHash can execute parallel calls
	// to checkSumDB.

	// Check whether mod+h is listed in go.sum already. If so, we're done.
	f.mu.Lock()
	inited, err := f.initGoSum()
	if err != nil {
		f.mu.Unlock()
		return err
	}
	done := inited && haveModSumLocked(f, mod, h)
	if inited {
		st := f.sumState.status[modSum{mod, h}]
		st.used = true
		f.sumState.status[modSum{mod, h}] = st
	}
	f.mu.Unlock()

	if done {
		return nil
	}

	// Not listed, so we want to add them.
	// Consult checksum database if appropriate. It knows no git sums.
	if useSumDB(mod) && !IsGitSum(h) {
		// Calls base.Fatalf if mismatch detected.
		if err := checkSumDB(mod, h); err != nil {
			return err
		}
	}

	// Add mod+h to go.sum, if it hasn't appeared already.
	if inited {
		f.mu.Lock()
		addModSumLocked(f, mod, h)
		st := f.sumState.status[modSum{mod, h}]
		st.dirty = true
		f.sumState.status[modSum{mod, h}] = st
		f.mu.Unlock()
	}
	return nil
}

// haveModSumLocked reports whether the pair mod,h is already listed in go.sum.
// If it finds a conflicting pair instead, it calls base.Fatalf.
// goSum.mu must be locked.
func haveModSumLocked(f *Fetcher, mod module.Version, h string) bool {
	sumFileName := "go.sum"
	if strings.HasSuffix(f.goSumFile, "go.work.sum") {
		sumFileName = "go.work.sum"
	}
	for _, vh := range f.sumState.m[mod] {
		if h == vh {
			return true
		}
		if knownSum(vh) && sameSumKind(h, vh) {
			base.Fatalf("verifying %s@%s: checksum mismatch\n\tdownloaded: %v\n\t%s:     %v"+goSumMismatch, mod.Path, mod.Version, h, sumFileName, vh)
		}
	}
	// Also check workspace sums.
	foundMatch := false
	// Check sums from all files in case there are conflicts between
	// the files.
	for goSumFile, goSums := range f.sumState.w {
		for _, vh := range goSums[mod] {
			if h == vh {
				foundMatch = true
			} else if knownSum(vh) && sameSumKind(h, vh) {
				base.Fatalf("verifying %s@%s: checksum mismatch\n\tdownloaded: %v\n\t%s:     %v"+goSumMismatch, mod.Path, mod.Version, h, goSumFile, vh)
			}
		}
	}
	return foundMatch
}

// addModSumLocked adds the pair mod,h to go.sum.
// goSum.mu must be locked.
func addModSumLocked(f *Fetcher, mod module.Version, h string) {
	if haveModSumLocked(f, mod, h) {
		return
	}
	if slices.ContainsFunc(f.sumState.m[mod], func(vh string) bool { return !knownSum(vh) }) {
		fmt.Fprintf(os.Stderr, "warning: verifying %s@%s: unknown hashes in go.sum: %v; adding %v"+hashVersionMismatch, mod.Path, mod.Version, strings.Join(f.sumState.m[mod], ", "), h)
	}
	f.sumState.m[mod] = append(f.sumState.m[mod], h)
}

// checkSumDB checks the mod, h pair against the Go checksum database.
// It calls base.Fatalf if the hash is to be rejected.
func checkSumDB(mod module.Version, h string) error {
	modWithoutSuffix := mod
	noun := "module"
	if before, found := strings.CutSuffix(mod.Version, "/go.mod"); found {
		noun = "go.mod"
		modWithoutSuffix.Version = before
	}

	db, lines, err := lookupSumDB(mod)
	if err != nil {
		return module.VersionError(modWithoutSuffix, fmt.Errorf("verifying %s: %v", noun, err))
	}

	have := mod.Path + " " + mod.Version + " " + h
	prefix := mod.Path + " " + mod.Version + " h1:"
	for _, line := range lines {
		if line == have {
			return nil
		}
		if strings.HasPrefix(line, prefix) {
			return module.VersionError(modWithoutSuffix, fmt.Errorf("verifying %s: checksum mismatch\n\tdownloaded: %v\n\t%s: %v"+sumdbMismatch, noun, h, db, line[len(prefix)-len("h1:"):]))
		}
	}
	return module.VersionError(modWithoutSuffix, fmt.Errorf("verifying %s: checksum missing from sumdb response"+sumdbAbsent, noun))
}

// Sum returns the checksum for the downloaded copy of the given module,
// if present in the download cache.
func Sum(ctx context.Context, mod module.Version) string {
	if cfg.GOMODCACHE == "" {
		// Do not use current directory.
		return ""
	}

	ziphash, err := CachePath(ctx, mod, "ziphash")
	if err != nil {
		return ""
	}
	data, err := lockedfile.Read(ziphash)
	if err != nil {
		return ""
	}
	data = bytes.TrimSpace(data)
	if !isValidSum(data) {
		return ""
	}
	return string(data)
}

// isValidSum returns true if data is the valid contents of a zip hash file.
// Certain critical files are written to disk by first truncating
// then writing the actual bytes, so that if the write fails
// the corrupt file should contain at least one of the null
// bytes written by the truncate operation.
func isValidSum(data []byte) bool {
	if bytes.IndexByte(data, '\000') >= 0 {
		return false
	}
	if IsGitSum(string(data)) {
		return true
	}

	if len(data) != len("h1:")+base64.StdEncoding.EncodedLen(sha256.Size) {
		return false
	}

	return true
}

// gitSumPrefix starts a git sum: the commit a GitHub archive holds, as
// "git:<40 hex digits>". GitHub is trusted to serve that commit, so the commit
// alone identifies the files and nothing hashes them.
const gitSumPrefix = "git:"

// IsGitSum reports whether h is a git sum.
func IsGitSum(h string) bool {
	hash, ok := strings.CutPrefix(h, gitSumPrefix)
	return ok && len(hash) == 40 && codehost.AllHex(hash)
}

// knownSum reports whether h is a checksum this go command can check: an h1
// sum, as every go command writes, or a git sum.
func knownSum(h string) bool {
	return strings.HasPrefix(h, "h1:") || IsGitSum(h)
}

// sameSumKind reports whether a and b are sums of one kind, so that a
// difference between them is a mismatch. An h1 sum and a git sum of one module
// never contradict each other.
func sameSumKind(a, b string) bool {
	return IsGitSum(a) == IsGitSum(b)
}

// recordsH1 reports whether a go.sum records an h1 sum for mod. A module read
// from a GitHub archive is then checked against it, so a go.sum that another go
// command wrote keeps working.
func (f *Fetcher) recordsH1(mod module.Version) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	inited, err := f.initGoSum()
	if err != nil || !inited {
		return false
	}
	for _, h := range f.sumState.m[mod] {
		if strings.HasPrefix(h, "h1:") {
			return true
		}
	}
	for _, goSums := range f.sumState.w {
		for _, h := range goSums[mod] {
			if strings.HasPrefix(h, "h1:") {
				return true
			}
		}
	}
	return false
}

var ErrGoSumDirty = errors.New("updates to go.sum needed, disabled by -mod=readonly")

// WriteGoSum writes the go.sum file if it needs to be updated.
//
// keep is used to check whether a newly added sum should be saved in go.sum.
// It should have entries for both module content sums and go.mod sums
// (version ends with "/go.mod"). Existing sums will be preserved unless they
// have been marked for deletion with TrimGoSum.
func (f *Fetcher) WriteGoSum(ctx context.Context, keep map[module.Version]bool, readonly bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// If we haven't read the go.sum file yet, don't bother writing it.
	if !f.sumState.enabled {
		return nil
	}

	// Check whether we need to add sums for which keep[m] is true or remove
	// unused sums marked with TrimGoSum. If there are no changes to make,
	// just return without opening go.sum.
	dirty := false
Outer:
	for m, hs := range f.sumState.m {
		for _, h := range hs {
			st := f.sumState.status[modSum{m, h}]
			if st.dirty && (!st.used || keep[m]) {
				dirty = true
				break Outer
			}
		}
	}
	if !dirty {
		return nil
	}
	if readonly {
		return ErrGoSumDirty
	}
	if fsys.Replaced(f.goSumFile) {
		base.Fatalf("go: updates to go.sum needed, but go.sum is part of the overlay specified with -overlay")
	}

	// Make a best-effort attempt to acquire the side lock, only to exclude
	// previous versions of the 'go' command from making simultaneous edits.
	if unlock, err := SideLock(ctx); err == nil {
		defer unlock()
	}

	err := lockedfile.Transform(f.goSumFile, func(data []byte) ([]byte, error) {
		tidyGoSum := tidyGoSum(f, data, keep)
		return tidyGoSum, nil
	})
	if err != nil {
		return fmt.Errorf("updating go.sum: %w", err)
	}

	f.sumState.status = make(map[modSum]modSumStatus)
	f.sumState.overwrite = false
	return nil
}

// TidyGoSum returns a tidy version of the go.sum file.
// A missing go.sum file is treated as if empty.
func (f *Fetcher) TidyGoSum(keep map[module.Version]bool) (before, after []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	before, err := lockedfile.Read(f.goSumFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		base.Fatalf("reading go.sum: %v", err)
	}
	after = tidyGoSum(f, before, keep)
	return before, after
}

// tidyGoSum returns a tidy version of the go.sum file.
// The goSum lock must be held.
func tidyGoSum(f *Fetcher, data []byte, keep map[module.Version]bool) []byte {
	if !f.sumState.overwrite {
		// Incorporate any sums added by other processes in the meantime.
		// Add only the sums that we actually checked: the user may have edited or
		// truncated the file to remove erroneous hashes, and we shouldn't restore
		// them without good reason.
		f.sumState.m = make(map[module.Version][]string, len(f.sumState.m))
		readGoSum(f.sumState.m, f.goSumFile, data)
		for ms, st := range f.sumState.status {
			if st.used && !sumInWorkspaceModulesLocked(f, ms.mod) {
				addModSumLocked(f, ms.mod, ms.sum)
			}
		}
	}

	mods := make([]module.Version, 0, len(f.sumState.m))
	for m := range f.sumState.m {
		mods = append(mods, m)
	}
	module.Sort(mods)

	var buf bytes.Buffer
	for _, m := range mods {
		// An org module has no sum, so go.sum never grows a line for one, and a
		// line a previous command or a previous go command left behind is
		// dropped here.
		if orgmod.IsOrg(m.Path) {
			continue
		}
		list := f.sumState.m[m]
		sort.Strings(list)
		str.Uniq(&list)
		for _, h := range list {
			st := f.sumState.status[modSum{m, h}]
			if (!st.dirty || (st.used && keep[m])) && !sumInWorkspaceModulesLocked(f, m) {
				fmt.Fprintf(&buf, "%s %s %s\n", m.Path, m.Version, h)
			}
		}
	}
	return buf.Bytes()
}

func sumInWorkspaceModulesLocked(f *Fetcher, m module.Version) bool {
	for _, goSums := range f.sumState.w {
		if _, ok := goSums[m]; ok {
			return true
		}
	}
	return false
}

// TrimGoSum trims go.sum to contain only the modules needed for reproducible
// builds.
//
// keep is used to check whether a sum should be retained in go.mod. It should
// have entries for both module content sums and go.mod sums (version ends
// with "/go.mod").
func (f *Fetcher) TrimGoSum(keep map[module.Version]bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inited, err := f.initGoSum()
	if err != nil {
		base.Fatalf("%s", err)
	}
	if !inited {
		return
	}

	for m, hs := range f.sumState.m {
		if !keep[m] {
			for _, h := range hs {
				f.sumState.status[modSum{m, h}] = modSumStatus{used: false, dirty: true}
			}
			f.sumState.overwrite = true
		}
	}
}

const goSumMismatch = `

SECURITY ERROR
This download does NOT match an earlier download recorded in go.sum.
The bits may have been replaced on the origin server, or an attacker may
have intercepted the download attempt.

For more information, see 'go help module-auth'.
`

const sumdbMismatch = `

SECURITY ERROR
This download does NOT match the one reported by the checksum server.
The bits may have been replaced on the origin server, or an attacker may
have intercepted the download attempt.

For more information, see 'go help module-auth'.
`

const sumdbAbsent = `

SECURITY ERROR
This download does NOT match one reported by the checksum server.
The checksum server has provided checksums, but the checksums do
not contain an entry for the download.
The checksum server may be malfunctioning, or an attacker may have
intercepted the checksum request.
The download cannot be verified.

For more information, see 'go help module-auth'.
`

const hashVersionMismatch = `

SECURITY WARNING
This download is listed in go.sum, but using an unknown hash algorithm.
The download cannot be verified.

For more information, see 'go help module-auth'.

`

var HelpModuleAuth = &base.Command{
	UsageLine: "module-auth",
	Short:     "module authentication using go.sum",
	Long: `
When the go command downloads a module zip file or go.mod file into the
module cache, it computes a cryptographic hash and compares it with a known
value to verify the file hasn't changed since it was first downloaded. Known
hashes are stored in a file in the module root directory named go.sum. Hashes
may also be downloaded from the checksum database depending on the values of
GOSUMDB, GOPRIVATE, and GONOSUMDB.

For details, see https://go.dev/ref/mod#authenticating.
`,
}

var HelpPrivate = &base.Command{
	UsageLine: "private",
	Short:     "configuration for downloading non-public code",
	Long: `
The go command defaults to downloading modules from the public Go module
mirror at proxy.golang.org. It also defaults to validating downloaded modules,
regardless of source, against the public Go checksum database at sum.golang.org.
These defaults work well for publicly available source code.

The GOPRIVATE environment variable controls which modules the go command
considers to be private (not available publicly) and should therefore not use
the proxy or checksum database. The variable is a comma-separated list of
glob patterns (in the syntax of Go's path.Match) of module path prefixes.
For example,

	GOPRIVATE=*.corp.example.com,rsc.io/private

causes the go command to treat as private any module with a path prefix
matching either pattern, including git.corp.example.com/xyzzy, rsc.io/private,
and rsc.io/private/quux.

For fine-grained control over module download and validation, the GONOPROXY
and GONOSUMDB environment variables accept the same kind of glob list
and override GOPRIVATE for the specific decision of whether to use the proxy
and checksum database, respectively.

For example, if a company ran a module proxy serving private modules,
users would configure go using:

	GOPRIVATE=*.corp.example.com
	GOPROXY=proxy.example.com
	GONOPROXY=none

The GOPRIVATE variable is also used to define the "public" and "private"
patterns for the GOVCS variable; see 'go help vcs'. For that usage,
GOPRIVATE applies even in GOPATH mode. In that case, it matches import paths
instead of module paths.

The 'go env -w' command (see 'go help env') can be used to set these variables
for future go command invocations.

For more details, see https://go.dev/ref/mod#private-modules.
`,
}
