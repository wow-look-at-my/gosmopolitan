// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"bytes"
	"io"
	"testing"

	"cmd/go/internal/trace"
)

// execOnly is a Cache that can store an executable.
type execOnly struct{ Cache }

func (execOnly) PutExecutable(ActionID, string, io.ReadSeeker) (OutputID, int64, error) {
	return OutputID{}, 0, nil
}

// plainOnly is a Cache that cannot, which is what a cache program is: its
// protocol has no such operation.
type plainOnly struct{ Cache }

// A traced cache must answer the ExecutableCache assertion exactly when the
// cache under it does. Answering it always makes the caller store an
// executable through a cache that cannot; answering it never turns
// executable caching off for every traced build, silently, since the caller
// tests the assertion and moves on.
func TestTracedPreservesExecutableCache(t *testing.T) {
	// An inert lane returns the cache unchanged, so this needs a live one.
	ctx, done, err := trace.Start(t.Context(), t.TempDir()+"/trace.json")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	lane := trace.LaneOf(ctx)
	if !lane.Enabled() {
		t.Fatal("trace.Start gave a lane that records nothing")
	}

	if _, ok := Traced(execOnly{}, lane).(ExecutableCache); !ok {
		t.Error("a traced ExecutableCache is not an ExecutableCache")
	}
	if _, ok := Traced(plainOnly{}, lane).(ExecutableCache); ok {
		t.Error("a traced plain Cache reports that it can store an executable")
	}
}

// An inert lane must leave the cache alone: an untraced build pays one
// comparison, not an indirection on every lookup.
func TestTracedIsIdentityWithoutALane(t *testing.T) {
	c := plainOnly{}
	if got := Traced(c, trace.Lane{}); got != Cache(c) {
		t.Errorf("Traced with no lane returned %T, want the cache it was handed", got)
	}
}

// The disk cache reports its own tier, which is what lets a trace tell a hit
// served off local disk from one fetched over the network.
func TestDiskCacheReportsItsTier(t *testing.T) {
	c, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := ActionID{1, 2, 3}
	if _, _, err := c.Put(id, bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatal(err)
	}
	entry, tier, err := c.getTiered(id)
	if err != nil {
		t.Fatal(err)
	}
	if tier != tierDisk {
		t.Errorf("tier = %q, want %q", tier, tierDisk)
	}
	if entry.Size != 5 {
		t.Errorf("entry.Size = %d, want 5", entry.Size)
	}
}
