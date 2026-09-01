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
	"sync"

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
	// The client writes diagnostics nowhere until a consumer says otherwise,
	// and cmd/go's stderr is where a build's warnings already go.
	cacheclient.SetLogger(goLogger{})
	remote, err := cacheclient.NewWebBackend(cfg)
	if err != nil || remote == nil {
		// A shared cache that cannot be reached is a slower build, not a
		// broken one. Say so once; do not fail the build over it.
		if err != nil {
			fmt.Fprintf(os.Stderr, "go: shared build cache disabled: %v\n", err)
		}
		return nil
	}
	return &SharedCache{DiskCache: disk, remote: remote}
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
	outputID, exeName, body, size, _, miss, rerr := c.remote.GetExecutable(actionID)
	if miss || rerr != nil || body == nil {
		return Entry{}, tierShared, err // the local miss, which is what the caller expects
	}
	defer body.Close()

	// The client has already verified this body against its outputID and its
	// build id. Storing it locally re-verifies it on the way in and gives the
	// compiler a path to open.
	data, readErr := io.ReadAll(body)
	if readErr != nil {
		return Entry{}, tierShared, err
	}
	var gotID OutputID
	var n int64
	var putErr error
	if exeName != "" {
		// An executable entry must land in the shape the build can fork/exec:
		// a directory holding a 0777 file of this name, not a 0666 regular
		// file. A plain Put here is exactly the regression this tier shipped
		// with: the hit exists, useCache trusts it, go run dies with
		// permission denied.
		gotID, n, putErr = c.DiskCache.PutExecutable(id, exeName, bytes.NewReader(data))
	} else {
		gotID, n, putErr = c.DiskCache.Put(id, bytes.NewReader(data))
	}
	if putErr != nil {
		return Entry{}, tierShared, err
	}
	if want, decErr := decodeOutputID(outputID); decErr == nil && gotID != want {
		// The shared tier named an outputID the stored body does not have.
		// Serving it would hand the compiler the wrong object under this key.
		return Entry{}, tierShared, err
	}
	if size > 0 && n != size {
		return Entry{}, tierShared, err
	}
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
	c.offer(id, outputID, size)
	return outputID, size, nil
}

// PutExecutable mirrors Put for an output the build will execute. The
// executable's name rides the shared tier's metadata, because the name is
// the only carrier of what the body is: a cosmo APE starts with a '#!'
// shell header, so no byte sniff could recover it on the way back.
func (c *SharedCache) PutExecutable(id ActionID, name string, file io.ReadSeeker) (OutputID, int64, error) {
	outputID, size, err := c.DiskCache.PutExecutable(id, name, file)
	if err != nil {
		return outputID, size, err
	}
	f, err := os.Open(c.DiskCache.OutputFile(outputID))
	if err == nil {
		_ = c.remote.PutExecutable(hex.EncodeToString(id[:]), hex.EncodeToString(outputID[:]), name, f, size)
		f.Close()
	}
	return outputID, size, nil
}

// offer uploads the stored body. It reads back the file the DiskCache just
// wrote rather than rewinding the caller's reader: the caller owns that reader
// and the contract does not promise it is still seekable afterwards.
func (c *SharedCache) offer(id ActionID, outputID OutputID, size int64) {
	f, err := os.Open(c.DiskCache.OutputFile(outputID))
	if err != nil {
		return
	}
	defer f.Close()
	_ = c.remote.Put(hex.EncodeToString(id[:]), hex.EncodeToString(outputID[:]), f, size)
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
