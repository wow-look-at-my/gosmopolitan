// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package cache

import (
	"testing"
	"time"

	"cmd/go/internal/base"

	"github.com/wow-look-at-my/go-s3-server/cacheclient"
)

// A cache that starts to open after the go command began to exit serves no
// broker: no AtExit function would remain to unlink the broker's names, and
// they would outlive the process.
func TestNoBrokerOnceExitBegins(t *testing.T) {
	t.Fork() // the exit state and the broker are process-wide
	// The go test that started this binary names its own broker. This process must be an owner to serve one.
	t.Setenv(cacheclient.BrokerEnv, "")
	dir := t.TempDir()
	base.RunAtExit()
	returned := make(chan struct{})
	go func() {
		openCache(dir)
		close(returned)
	}()
	select {
	case <-returned:
		t.Fatal("openCache went on after the exit began")
	case <-time.After(200 * time.Millisecond):
	}
	if env := cacheclient.BrokerEnviron(); env != nil {
		t.Fatalf("a broker was served after the exit began: %q", env)
	}
}
