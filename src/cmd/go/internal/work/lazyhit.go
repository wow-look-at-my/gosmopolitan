// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"cmd/go/internal/cache"
	"cmd/go/internal/cfg"
	"cmd/internal/buildid"
)

// A compile hit needs the build ID and the export ID of the archive, not
// its bytes. A cached test result needs nothing more from the packages under
// it, so a build that reuses one never reads those archives. Each compile
// therefore stores a small record beside its archive, and a hit reads that
// record. The archive is fetched only when an action reads its bytes.

// lazyKey is the cache key of the lazy record of the compile with action ID id.
func lazyKey(id cache.ActionID) cache.ActionID {
	return cache.Subkey(id, "lazy")
}

// lazyOutput is an archive that a lazy hit named and did not fetch.
type lazyOutput struct {
	c      cache.Cache
	action cache.ActionID
	a      *Action
}

// lazyEnabled reports whether a can hit through its lazy record. A listing
// that reports the archive's path needs the file at that path. An install
// puts its archives in the local cache, where a go command with no shared
// store, go_bootstrap in make.bash for one, reads them.
func (b *Builder) lazyEnabled(a *Action) bool {
	return a.Mode == "build" && cfg.BuildToolchainName == "gc" && !b.NeedExport && cfg.CmdName != "install"
}

// storeLazy writes the lazy record of a, whose archive the cache stored as
// outputID.
func storeLazy(c cache.Cache, a *Action, outputID cache.OutputID, size int64) {
	record := strings.Join([]string{a.buildID, a.exportID, hex.EncodeToString(outputID[:]), strconv.FormatInt(size, 10)}, " ")
	cache.PutBytes(c, lazyKey(a.actionID), []byte(record))
}

// lazyRecord is what a lazy record holds.
type lazyRecord struct {
	file     string
	buildID  string
	exportID string
}

// readLazy reads the lazy record of a. It fetches nothing else.
func readLazy(a *Action, c cache.Cache) (lazyRecord, bool) {
	data, _, err := cache.GetBytes(c, lazyKey(a.actionID))
	if err != nil {
		return lazyRecord{}, false
	}
	fields := strings.Fields(string(data))
	if len(fields) != 4 {
		return lazyRecord{}, false
	}
	raw, err := hex.DecodeString(fields[2])
	var outputID cache.OutputID
	if err != nil || len(raw) != len(outputID) {
		return lazyRecord{}, false
	}
	copy(outputID[:], raw)
	return lazyRecord{file: c.OutputFile(outputID), buildID: fields[0], exportID: fields[1]}, true
}

// adoptLazy makes a hit of a from record. The archive stays where it is
// until an action reads it.
func (b *Builder) adoptLazy(a *Action, c cache.Cache, record lazyRecord) {
	b.id.Lock()
	b.buildIDCache[record.file] = record.buildID
	if _, err := os.Stat(record.file); err != nil {
		b.lazy[record.file] = lazyOutput{c: c, action: a.actionID, a: a}
	}
	b.id.Unlock()
	a.buildID = record.buildID
	a.exportID = record.exportID
	a.built = record.file
}

// fetchLazy makes sure the archive at file is on disk, when a lazy hit named
// it without fetching it.
func (b *Builder) fetchLazy(file string) error {
	b.id.Lock()
	lazy, ok := b.lazy[file]
	b.id.Unlock()
	if !ok {
		return nil
	}
	_, err := b.lazyFetch.Do(file, func() (struct{}, error) {
		got, _, err := cache.FetchFile(lazy.c, lazy.action)
		if err != nil {
			// The store's index can name an entry the server has evicted. The
			// package compiles again, and a reproducible compile lands at file.
			if rebuildErr := b.rebuildLazy(lazy.a); rebuildErr != nil {
				return struct{}{}, fmt.Errorf("the build cache named %s for a hit but cannot supply it: %w; compiling it again failed: %v", file, err, rebuildErr)
			}
			got = file
			if _, statErr := os.Stat(file); statErr != nil {
				return struct{}{}, fmt.Errorf("the build cache named %s for a hit but cannot supply it: %w; compiling it again wrote %s", file, err, lazy.a.built)
			}
		}
		if got != file {
			return struct{}{}, fmt.Errorf("the build cache supplied %s for a hit that named %s", got, file)
		}
		b.id.Lock()
		delete(b.lazy, file)
		b.id.Unlock()
		return struct{}{}, nil
	})
	return err
}

// rebuildLazy compiles a again, after a lazy hit named an archive the cache
// cannot supply. The hit set a to its cached build ID and told its build to do
// nothing, so both go back to how a miss leaves them. A package whose build
// needs the cgo or cover work the hit skipped is not compiled again.
func (b *Builder) rebuildLazy(a *Action) error {
	if a == nil || a.Package == nil {
		return errors.New("no action to compile")
	}
	if p := a.Package; p.UsesCgo() || p.UsesSwig() || p.Internal.Cover.Mode != "" {
		return fmt.Errorf("%s needs the cgo or cover steps a hit skips", p.ImportPath)
	}
	var provider *checkCacheProvider
	for _, dep := range a.Deps {
		if pr, ok := dep.Provider.(*checkCacheProvider); ok {
			provider = pr
		}
	}
	if provider == nil {
		return errors.New("no cache check to send back")
	}
	actionID := buildid.HashToString(a.actionID)
	a.buildID = actionID + buildIDSeparator + actionID
	a.output = []byte{}
	provider.need = needBuild
	if err := b.Shell(a).Mkdir(a.Objdir); err != nil {
		return err
	}
	return b.build(context.Background(), a)
}

// fetchLazyDeps is fetchLazy for the archive of each action in deps. The
// fetches run at the same time, so the cache can send them in one batch.
func (b *Builder) fetchLazyDeps(deps []*Action) error {
	files := make([]string, len(deps))
	for idx, dep := range deps {
		files[idx] = dep.built
	}
	return b.fetchLazyFiles(files)
}

// fetchLazyFiles is fetchLazy for each of files, at the same time.
func (b *Builder) fetchLazyFiles(files []string) error {
	errs := make([]error, len(files))
	var wg sync.WaitGroup
	for idx, file := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[idx] = b.fetchLazy(file)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
