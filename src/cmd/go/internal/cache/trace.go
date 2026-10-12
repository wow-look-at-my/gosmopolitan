// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package cache

import (
	"encoding/hex"
	"io"
	"time"

	"cmd/go/internal/trace"

	"github.com/wow-look-at-my/go-s3-server/cacheclient/cachedisk"
)

// Tracing the cache.

// Traced returns c recording every operation onto lane.
func Traced(c Cache, lane trace.Lane) Cache {
	if c == nil || !lane.Enabled() {
		return c
	}
	return &tracedCache{Cache: c, lane: lane}
}

type tracedCache struct {
	Cache
	lane trace.Lane
}

func (c *tracedCache) Get(id ActionID) (Entry, error) {
	start := time.Now()
	var (
		entry Entry
		err   error
		tier  = cachedisk.TierDisk
	)
	if reporter, ok := c.Cache.(cachedisk.Tiered); ok {
		entry, tier, err = reporter.GetTiered(id)
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
