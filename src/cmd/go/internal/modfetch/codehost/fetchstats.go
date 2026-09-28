// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package codehost

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"cmd/go/internal/web"
)

// A Fetch records the route that served one module version, and the bytes and
// time summed over each attempt that received data.
type Fetch struct {
	mu       sync.Mutex
	route    string
	failures []string
	bytes    int64
	transfer time.Duration
	first    time.Time
}

// AddFailure records that source was tried before the route and failed.
func (rec *Fetch) AddFailure(source string, err error) {
	if rec == nil {
		return
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.failures = append(rec.failures, source+": "+ShortError(err))
}

// ShortError is err as its HTTP status, or else as its first line without
// the URL that failed.
func ShortError(err error) string {
	var httpErr *web.HTTPError
	if errors.As(err, &httpErr) && httpErr.Status != "" {
		return httpErr.Status
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	} else if inner := errors.Unwrap(err); inner != nil {
		err = inner
	}
	text, _, _ := strings.Cut(err.Error(), "\n")
	return text
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

// Stats returns the route with the failures before it, the bytes received,
// the wall time of the transfers, and when the earliest transfer began.
func (rec *Fetch) Stats() (route string, size int64, transfer time.Duration, first time.Time) {
	if rec == nil {
		return "", 0, 0, time.Time{}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	route = rec.route
	if route != "" && len(rec.failures) > 0 {
		route += ", because " + strings.Join(rec.failures, "; ")
	}
	return route, rec.bytes, rec.transfer, rec.first
}

// take copies the route and the failures of from into rec. With transfers
// set, it moves the transfers too.
func (rec *Fetch) take(from *Fetch, transfers bool) {
	from.mu.Lock()
	route, failures := from.route, slices.Clone(from.failures)
	size, took, first := from.bytes, from.transfer, from.first
	from.mu.Unlock()
	rec.mu.Lock()
	rec.route = route
	rec.failures = append(rec.failures, failures...)
	rec.mu.Unlock()
	if transfers {
		rec.AddTransfer(first, size, took)
	}
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
	dst.take(kept.rec, !kept.claimed)
	kept.claimed = true
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
