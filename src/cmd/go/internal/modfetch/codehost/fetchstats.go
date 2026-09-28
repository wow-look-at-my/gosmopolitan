// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package codehost

import (
	"context"
	"io/fs"
	"path/filepath"
	"sync"
	"time"
)

// A Fetch records the route that served one module version, and the bytes and
// time summed over each attempt that received data.
type Fetch struct {
	mu       sync.Mutex
	route    string
	bytes    int64
	transfer time.Duration
	first    time.Time
}

type fetchKey struct{}

// WithFetch returns a context whose downloads record into rec.
func WithFetch(ctx context.Context, rec *Fetch) context.Context {
	return context.WithValue(ctx, fetchKey{}, rec)
}

// FetchFrom returns the record that ctx carries, or nil.
func FetchFrom(ctx context.Context) *Fetch {
	rec, _ := ctx.Value(fetchKey{}).(*Fetch)
	return rec
}

// SetRoute names the source that served the module.
func (rec *Fetch) SetRoute(route string) {
	if rec == nil {
		return
	}
	rec.mu.Lock()
	rec.route = route
	rec.mu.Unlock()
}

// AddTransfer records one attempt that began at start, received size bytes
// and took took.
func (rec *Fetch) AddTransfer(start time.Time, size int64, took time.Duration) {
	if rec == nil || size <= 0 {
		return
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.bytes += size
	rec.transfer += took
	if rec.first.IsZero() || start.Before(rec.first) {
		rec.first = start
	}
}

// Stats returns the route, the bytes received, the wall time of the
// transfers, and when the earliest transfer began.
func (rec *Fetch) Stats() (route string, size int64, transfer time.Duration, first time.Time) {
	if rec == nil {
		return "", 0, 0, time.Time{}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.route, rec.bytes, rec.transfer, rec.first
}

// add moves the transfers of from into rec.
func (rec *Fetch) add(from *Fetch) {
	_, size, took, first := from.Stats()
	rec.AddTransfer(first, size, took)
}

// A keptFetch holds a commit's fetch until the module's download claims it,
// because Stat can fetch a commit before the download begins.
type keptFetch struct {
	rec     *Fetch
	claimed bool
}

// keepFetch remembers rec as the fetch of hash.
func (r *gitRepo) keepFetch(hash string, rec *Fetch) {
	r.fetchMu.Lock()
	defer r.fetchMu.Unlock()
	if r.fetched == nil {
		r.fetched = make(map[string]*keptFetch)
	}
	r.fetched[hash] = &keptFetch{rec: rec}
}

// claimFetch copies the fetch of hash into the record ctx carries. Only the
// first claim takes the transfers, so modules at one commit do not both
// report its bytes. With no fetch in this process, the route is fallback.
func (r *gitRepo) claimFetch(ctx context.Context, hash, fallback string) {
	dst := FetchFrom(ctx)
	if dst == nil {
		return
	}
	r.fetchMu.Lock()
	defer r.fetchMu.Unlock()
	kept := r.fetched[hash]
	if kept == nil {
		dst.SetRoute(fallback)
		return
	}
	route, _, _, _ := kept.rec.Stats()
	dst.SetRoute(route)
	if !kept.claimed {
		kept.claimed = true
		dst.add(kept.rec)
	}
}

// objectBytes is the size of the git object store. Its growth over a git
// fetch stands in for the bytes the fetch received: git keeps a received pack
// as it came, or unpacks a small one into compressed loose objects.
func (r *gitRepo) objectBytes() int64 {
	var total int64
	filepath.WalkDir(filepath.Join(r.dir, "objects"), func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
