// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"os"
	"testing"
)

// The death-signal helpers are this test binary, run again. Without these
// hooks a helper runs the whole package and starts its own helper.
func TestMain(m *testing.M) {
	if os.Getenv("GO_DEATHSIG_PARENT") == "1" {
		deathSignalParent()
	} else if os.Getenv("GO_DEATHSIG_CHILD") == "1" {
		deathSignalChild()
	}

	os.Exit(m.Run())
}
