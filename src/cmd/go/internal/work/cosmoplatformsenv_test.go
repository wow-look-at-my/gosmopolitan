// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import "testing"

// Every GOCOSMO* variable reports its effective value, and cmd/go publishes
// each reported value back into its own environment so the tools it invokes
// agree with it. That is only safe for a value that parses back to itself.
// GOCOSMOPLATFORMS reports every platform when the caller chose none, which
// parses back as a deliberate selection of all of them -- so it is the one
// that must be withheld.
func TestOnlyGOCOSMOPLATFORMSIsWithheldFromTheEnvironment(t *testing.T) {
	if EnvSelfPublishable(CosmoPlatformsEnv) {
		t.Errorf("%s is published: an unset selection reads back as a choice of every platform",
			CosmoPlatformsEnv)
	}
	for _, name := range []string{"GOCOSMOFAT", "GOCOSMOSTRIP", "GOCOSMODEBUG", "GOOS", "GOARCH"} {
		if !EnvSelfPublishable(name) {
			t.Errorf("%s is withheld, so the tools cmd/go invokes no longer agree with it", name)
		}
	}
}
