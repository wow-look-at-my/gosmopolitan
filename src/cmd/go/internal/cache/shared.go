// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package cache

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	goCfg "cmd/go/internal/cfg"

	"github.com/wow-look-at-my/go-s3-server/cacheclient"
)

// SharedCache adds a shared, network-backed tier under the on-disk cache. It
// replaced GOCACHEPROG outright, which is deleted: the client is linked in
// and called directly, with no subprocess protocol left to name a program.
//
// The subprocess was never the point. GOCACHEPROG handed cmd/go a PATH
// (Response.DiskPath) rather than bytes, so a cache program that stores bodies
// in packed files has to materialize each hit somewhere the compiler can open
// it -- a loose mirror of every hit, or a FUSE mount serving the packs. In
// process there is no such requirement: a fetched body goes into the DiskCache
// below, which is the layout cmd/go already opens, mmaps and trims.
//
// The disk cache stays authoritative. The shared tier is consulted only on a
// local miss, and a fetched body is stored locally before it is returned, so
// OutputFile answers for a shared hit exactly as it does for a local one.
type SharedCache struct {
	*DiskCache
	remote *cacheclient.WebBackend

	closeOnce sync.Once
	closeErr  error
}

// sharedModule holds the main module's path for cache provenance.
//
// It is a variable rather than an argument because of when each side happens:
// the cache is built the first time anything asks for it, and the main module
// is not known until the module loader has run. Whichever comes first, the
// backend ends up with the path -- SetSharedModule reaches a backend that
// already exists, and newSharedCache reads the value for one built later.
var sharedModule atomic.Pointer[string]

// SetSharedModule records which module's build is running, for the shared
// cache's provenance headers. The module loader calls it once it knows.
func SetSharedModule(path string) {
	if path == "" {
		return
	}
	sharedModule.Store(&path)
	if c := liveShared.Load(); c != nil {
		c.remote.SetModule(path)
	}
}

// liveShared is the backend this process built, if it built one. Reaching it
// through Default() here would construct the cache as a side effect of naming
// a module, which is not what a setter may do.
var liveShared atomic.Pointer[SharedCache]

// mainModulePath is the recorded module path, or "" before the loader runs.
func mainModulePath() string {
	if p := sharedModule.Load(); p != nil {
		return *p
	}
	return ""
}

// Shared reports whether a shared cache tier is configured for this process.
func Shared() bool {
	return cacheclient.ConfigFromEnv().Bucket != ""
}

// validateCIShared fails a CI build that has no shared cache configured. A
// CI run's cache decides whether every other CI run recompiles the same
// packages, so an unconfigured CI run must never build quietly.
func validateCIShared() error {
	if os.Getenv("CI") == "" {
		return nil
	}
	if Shared() {
		return nil
	}
	return fmt.Errorf("CI build cache not configured: GO_BUILDCACHE_CONFIG is not set")
}

// newSharedCache layers the configured shared tier over disk. It returns nil
// when no shared cache is configured, which is the ordinary case for a
// developer's machine.
func newSharedCache(disk *DiskCache) Cache {
	cfg := cacheclient.ConfigFromEnv()
	if cfg.Bucket == "" {
		return nil
	}
	// Provenance. The environment names the endpoint and the credential; what
	// this build IS comes from the build itself, and the server has no other
	// way to learn it. Without these its log can say how many objects moved and
	// nothing about whose build moved them.
	cfg.Target = goCfg.Goos + "/" + goCfg.Goarch
	cfg.Version = runtime.Version()
	cfg.Module = mainModulePath()
	// The client writes diagnostics nowhere until a consumer says otherwise,
	// and cmd/go's stderr is where a build's warnings already go. The one
	// exception is the outage window below, which ends by itself.
	if !cacheQuiet() {
		cacheclient.SetLogger(goLogger{})
	}
	remote, err := newWebBackend(cfg)
	if err != nil || remote == nil {
		// A shared cache that cannot be reached is a slower build, not a
		// broken one. Say so once; do not fail the build over it.
		if err != nil {
			fmt.Fprintf(os.Stderr, "go: shared build cache disabled: %v\n", err)
		}
		return nil
	}
	c := &SharedCache{DiskCache: disk, remote: remote}
	// Without this the look-ahead pool has nowhere to put what it fetches, and
	// the client turns it off. This is the whole mechanism: objects land on
	// disk before the build asks for them, so the ask is a local read.
	remote.OnBatchEntries = c.populate
	liveShared.Store(c)
	return c
}

// populate stores objects the look-ahead pool fetched before the build asked
// for them. It runs on that pool's goroutines, several at a time.
//
// The cheap checks come first and the expensive one last. An object already on
// disk costs a stat here; an object that is not costs a decompress and a hash.
// Doing it the other way round would decompress the whole window on every
// request to discover the build already had it.
func (c *SharedCache) populate(entries []cacheclient.BatchEntry) {
	for _, e := range entries {
		actionID, ok := c.remote.ActionIDFromKey(e.Key)
		if !ok {
			continue
		}
		var id ActionID
		raw, err := hex.DecodeString(actionID)
		if err != nil || len(raw) != len(id) {
			continue
		}
		copy(id[:], raw)
		if _, err := c.DiskCache.Get(id); err == nil {
			continue // already local; nothing to do
		}
		data, ok := c.remote.Verify(e, actionID)
		if !ok {
			continue
		}
		out, decErr := decodeOutputID(e.OutputID)
		if decErr != nil {
			continue
		}
		// The client hashed this body to check it against its outputID a moment
		// ago. Put would hash it again to derive the same answer.
		c.putVerified(id, out, data)
	}
}

// putVerified writes a body whose OutputID is already known and already
// checked, skipping the hash Put would otherwise recompute.
func (c *SharedCache) putVerified(id ActionID, out OutputID, data []byte) {
	if err := c.DiskCache.copyFile(bytes.NewReader(data), out, int64(len(data))); err != nil {
		return
	}
	// allowVerify is false: this body came off the network, so the local
	// reproducibility check has nothing to say about it.
	_ = c.DiskCache.putIndexEntry(id, out, int64(len(data)), false)
}

// Get answers from disk, and asks the shared tier only when disk misses. A
// body the shared tier serves is written to disk before it is returned, so the
// Entry this hands back names a file that exists, like any other hit.
func (c *SharedCache) Get(id ActionID) (Entry, error) {
	entry, _, err := c.getTiered(id)
	return entry, err
}

// getTiered is Get, naming the tier that answered. A hit fetched over the
// network and a hit read off local disk cost different amounts of wall time
// and mean different things about the build, so a trace has to tell them
// apart.
func (c *SharedCache) getTiered(id ActionID) (Entry, string, error) {
	entry, err := c.DiskCache.Get(id)
	if err == nil {
		return entry, tierDisk, nil
	}

	actionID := hex.EncodeToString(id[:])
	outputID, data, _, miss := c.remote.Get(actionID)
	if miss || data == nil {
		return Entry{}, tierShared, err // the local miss, which is what the caller expects
	}

	// The client hashed this body against its outputID before answering, so
	// the value is known good and Put would only compute it a second time.
	out, decErr := decodeOutputID(outputID)
	if decErr != nil {
		return Entry{}, tierShared, err
	}
	c.putVerified(id, out, data)
	entry, err = c.DiskCache.Get(id)
	return entry, tierShared, err
}

// Put stores locally, then offers the body to the shared tier. The local store
// is what the build depends on, so its result is what Put reports; the upload
// is best effort and the client coalesces it with the rest of the build's.
func (c *SharedCache) Put(id ActionID, file io.ReadSeeker) (OutputID, int64, error) {
	outputID, size, err := c.DiskCache.Put(id, file)
	if err != nil {
		return outputID, size, err
	}
	c.offer(id, outputID)
	return outputID, size, nil
}

// offer uploads the stored body. It reads back the file the DiskCache just
// wrote rather than rewinding the caller's reader: the caller owns that reader
// and the contract does not promise it is still seekable afterwards.
//
// The read happens here and the compression does not. Put takes the bytes and
// returns, so the goroutine that just finished a compile goes back to
// compiling instead of spending its next milliseconds on lz4.
func (c *SharedCache) offer(id ActionID, outputID OutputID) {
	data, err := os.ReadFile(c.DiskCache.OutputFile(outputID))
	if err != nil {
		return
	}
	_ = c.remote.Put(hex.EncodeToString(id[:]), hex.EncodeToString(outputID[:]), data)
}

// Close drains the shared tier's in-flight uploads before the disk cache
// trims, so an upload never loses the file it is reading.
func (c *SharedCache) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = errors.Join(c.remote.Close(), c.DiskCache.Close())
	})
	return c.closeErr
}

// decodeOutputID parses the hex outputID the shared tier reports.
func decodeOutputID(s string) (OutputID, error) {
	var out OutputID
	raw, err := hex.DecodeString(s)
	if err != nil {
		return out, err
	}
	if len(raw) != len(out) {
		return out, fmt.Errorf("outputID is %d bytes, want %d", len(raw), len(out))
	}
	copy(out[:], raw)
	return out, nil
}

// cacheQuietUntil ends the outage window. The shared cache server answers 404
// for its index and for every upload, and the client reports each one, so a
// go command prints hundreds of lines it did not use to. Tests that read a go
// command's output then fail: cmd/internal/testdir asserts a go run prints
// nothing, and one of those lines landed inside a generated .go file.
//
// The window is a DEADLINE, not a switch, and it applies to CI alone. It
// expires on its own, and the diagnostics come back with no edit. Silencing
// this permanently would mean a broken cache nobody hears about; a date means
// somebody hears about it again on this day whether or not anyone remembered,
// and the CI condition means a developer never stops hearing about it.
//
// TestSharedCache_QuietWindowExpires pins that, so extending the date takes a
// deliberate edit to a test that says why.
//
// It moves because the outage is still on: the CI run that first tripped the
// deadline printed `web index fetch: HTTP 404` and a `web put ... HTTP 404`
// for every batch, on every go command in the job. Delete this and the gates
// below once a run comes back without them.
var cacheQuietUntil = time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)

// cacheQuiet reports whether to hold the shared tier's per-request
// diagnostics back.
//
// Only a CI run is ever quiet. CI is where the output-reading tests are, and
// it is the one place a person is not watching. A developer sees a failing
// cache the moment it fails, on every build, window or no window.
// GOCACHEDEBUG asks for them back anywhere.
func cacheQuiet() bool {
	if cacheDebug() {
		return false
	}
	if os.Getenv("CI") == "" {
		return false
	}
	return time.Now().Before(cacheQuietUntil)
}

// CacheDebugEnv asks for the shared tier's per-request diagnostics during the
// window above. Anything but the empty string enables them.
const CacheDebugEnv = "GOCACHEDEBUG"

// cacheDebug reports whether the caller asked to see those diagnostics.
func cacheDebug() bool {
	return os.Getenv(CacheDebugEnv) != ""
}

// newWebBackend builds the backend, during the window with os.Stderr pointed
// at the null device.
//
// The client aggregates its HTTP errors through a writer it captures ONCE,
// when the backend is built, and that writer is os.Stderr. SetLogger does not
// reach it, so the gate above covers only half the output. Swapping os.Stderr
// across this one call is what decides where those summaries go for the life
// of the backend. Narrow by construction: cmd/go builds exactly one backend,
// before it has anything else to say.
func newWebBackend(cfg cacheclient.WebConfig) (*cacheclient.WebBackend, error) {
	if !cacheQuiet() {
		return cacheclient.NewWebBackend(cfg)
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		// No null device is not a reason to give up a working cache.
		return cacheclient.NewWebBackend(cfg)
	}
	saved := os.Stderr
	os.Stderr = devnull
	defer func() { os.Stderr = saved }()
	return cacheclient.NewWebBackend(cfg)
}

// goLogger sends the client's diagnostics to stderr, where a build's warnings
// already go. cmd/go's stdout carries program output.
type goLogger struct{}

func (goLogger) Infof(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "go: "+format+"\n", args...)
}

func (goLogger) Warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "go: "+format+"\n", args...)
}

func (goLogger) Debugf(string, ...any) {}
