// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"bytes"
	"testing"

	"cmd/go/internal/trace"
)

// plainOnly is a bare Cache, which is every cache there is: one Put, one Get,
// one file per entry.
type plainOnly struct{ Cache }

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
