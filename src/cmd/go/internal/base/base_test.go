// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package base

import (
	"sync"
	"testing"
)

// Module loading resolves org module heads in parallel, and each one that
// resolves registers an exit function. Every registration must be kept, and
// none may be nil: Exit calls each entry.
func TestAtExitFromManyGoroutines(t *testing.T) {
	t.Serial()
	atExitMu.Lock()
	saved := atExitFuncs
	atExitFuncs = nil
	atExitMu.Unlock()
	t.Cleanup(func() {
		atExitMu.Lock()
		atExitFuncs = saved
		atExitMu.Unlock()
	})

	const workers = 64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			AtExit(func() {})
		}()
	}
	wg.Wait()

	atExitMu.Lock()
	defer atExitMu.Unlock()
	if len(atExitFuncs) != workers {
		t.Fatalf("AtExit kept %d of %d functions", len(atExitFuncs), workers)
	}
	for idx, fn := range atExitFuncs {
		if fn == nil {
			t.Fatalf("exit function %d is nil", idx)
		}
	}
}
