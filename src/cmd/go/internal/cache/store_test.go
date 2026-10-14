// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package cache

import (
	"testing"

	"github.com/wow-look-at-my/go-s3-server/cacheclient"
)

// The module loader can name the module while the cache opens. The store must
// learn the module in either order, or it fetches no build manifest.
func TestStoreLearnsTheModuleInEitherOrder(t *testing.T) {
	oldModule, oldStore := sharedModule.Load(), liveStore.Load()
	t.Cleanup(func() {
		sharedModule.Store(oldModule)
		liveStore.Store(oldStore)
	})
	for _, moduleFirst := range []bool{true, false} {
		sharedModule.Store(nil)
		liveStore.Store(nil)
		store, err := cacheclient.NewWebBackend(cacheclient.WebConfig{
			Bucket: "bucket", Endpoint: "http://127.0.0.1:1", AccessKey: "key", SecretKey: "secret", IndexDir: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if moduleFirst {
			SetSharedModule("example.com/m")
			publishStore(store)
		} else {
			publishStore(store)
			SetSharedModule("example.com/m")
		}
		if got := store.Module(); got != "example.com/m" {
			t.Errorf("module first %v: the store names module %q, want example.com/m", moduleFirst, got)
		}
	}
}
