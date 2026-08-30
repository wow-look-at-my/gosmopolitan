// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"encoding/hex"
	"io"
	"time"

	"cmd/go/internal/trace"
)

// Tracing the cache.
//
// A build's wall time is mostly two questions: what did it compile, and what
// did it not have to. The second one is invisible without this: a cache hit
// produces no subprocess, no output file the build wrote, and no line in any
// log. It shows up only as a gap.
//
// So every lookup and every store is recorded as its own slice, carrying the
// action ID it was keyed by, which tier answered, whether that tier had it,
// and how many bytes moved. A trace can then be read as "this action took
// 900ms and 890 of them were a compile, because the shared tier missed".
//
// The Cache interface takes no context, and it is reached from 91 call sites
// that would each have to grow one. Instead the caller wraps the handle once,
// for the lane it is running on: Traced returns a Cache that records onto that
// lane and is otherwise the cache it was handed.

// tiered is implemented by a Cache whose Get can report which tier answered.
// A cache program can put a network tier under the disk one, and a hit that
// crossed the network costs wall time a local one does not.
type tiered interface {
	getTiered(id ActionID) (entry Entry, tier string, err error)
}

// tierDisk is what the on-disk cache answers from.
const tierDisk = "disk"

// Traced returns c recording every operation onto lane. It returns c
// unchanged when the lane records nothing, so an untraced build pays one
// comparison and no indirection.
func Traced(c Cache, lane trace.Lane) Cache {
	if c == nil || !lane.Enabled() {
		return c
	}
	traced := &tracedCache{Cache: c, lane: lane}
	// The wrapper must answer the ExecutableCache assertion exactly when the
	// cache under it does. A wrapper that always carries the method makes the
	// assertion succeed over a cache that cannot store an executable; one that
	// never carries it turns executable caching off for every traced build.
	if exec, ok := c.(ExecutableCache); ok {
		return &tracedExecutableCache{tracedCache: traced, exec: exec}
	}
	return traced
}

type tracedCache struct {
	Cache
	lane trace.Lane
}

type tracedExecutableCache struct {
	*tracedCache
	exec ExecutableCache
}

func (c *tracedCache) Get(id ActionID) (Entry, error) {
	start := time.Now()
	var (
		entry Entry
		err   error
		tier  = tierDisk
	)
	if t, ok := c.Cache.(tiered); ok {
		entry, tier, err = t.getTiered(id)
	} else {
		entry, err = c.Cache.Get(id)
	}

	args := map[string]any{
		"action": hex.EncodeToString(id[:]),
		"tier":   tier,
	}
	if err != nil {
		args["outcome"] = "miss"
		args["error"] = err.Error()
	} else {
		args["outcome"] = "hit"
		args["output"] = hex.EncodeToString(entry.OutputID[:])
		args["size"] = entry.Size
	}
	c.lane.Since("cache get", "cache", start, args)
	return entry, err
}

func (c *tracedCache) Put(id ActionID, file io.ReadSeeker) (OutputID, int64, error) {
	start := time.Now()
	out, size, err := c.Cache.Put(id, file)
	c.lane.Since("cache put", "cache", start, putArgs(id, out, size, err))
	return out, size, err
}

func (c *tracedExecutableCache) PutExecutable(id ActionID, name string, file io.ReadSeeker) (OutputID, int64, error) {
	start := time.Now()
	out, size, err := c.exec.PutExecutable(id, name, file)
	args := putArgs(id, out, size, err)
	args["name"] = name
	c.lane.Since("cache put executable", "cache", start, args)
	return out, size, err
}

func putArgs(id ActionID, out OutputID, size int64, err error) map[string]any {
	args := map[string]any{
		"action": hex.EncodeToString(id[:]),
		"size":   size,
	}
	if err != nil {
		args["outcome"] = "error"
		args["error"] = err.Error()
	} else {
		args["outcome"] = "stored"
		args["output"] = hex.EncodeToString(out[:])
	}
	return args
}

// getTiered answers for the disk cache, which is the only tier it has.
func (c *DiskCache) getTiered(id ActionID) (Entry, string, error) {
	entry, err := c.Get(id)
	return entry, tierDisk, err
}
