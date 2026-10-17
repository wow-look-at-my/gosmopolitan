// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package work

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A fat build's sibling and merge use the store after Do returns, so Do must
// leave the cache open and the fatten step must close it.
func TestFatBuildClosesCacheAfterFatten(t *testing.T) {
	t.Serial()
	closes := 0
	saved := closeCache
	closeCache = func() { closes++ }
	t.Cleanup(func() { closeCache = saved })

	ctx := context.Background()
	builder := NewBuilder(t.TempDir(), nil)
	builder.HoldCache = true
	builder.Do(ctx, &Action{Mode: "go build"})
	require.Equal(t, 0, closes, "Do closed a held cache, so the sibling and the merge lose the store")

	cosmoFatten(ctx, builder, nil, nil)
	require.Equal(t, 1, closes, "the fatten step must close the cache it held")

	builder.HoldCache = false
	builder.Do(ctx, &Action{Mode: "go build"})
	require.Equal(t, 2, closes, "Do must still close a cache nobody holds")
}
