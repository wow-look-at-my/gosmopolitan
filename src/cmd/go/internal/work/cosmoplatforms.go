// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"os"
	"slices"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/internal/cosmoape"
)

// CosmoPlatformsEnv is the environment variable that restricts which host
// platforms a GOOS=cosmo build's APE boots on.
const CosmoPlatformsEnv = "GOCOSMOPLATFORMS"

// cosmoPlatformSpec returns the platform selection and whether it was set.
// An unset variable means every platform the payloads allow, which is what
// a cosmo build covered before the variable existed. An invalid value ends
// the build: covering more platforms than asked for, or silently dropping
// one, both ship a binary that does not match the request.
func cosmoPlatformSpec() (cosmoape.Set, bool) {
	v := os.Getenv(CosmoPlatformsEnv)
	if v == "" {
		return cosmoape.Default(), false
	}
	set, err := cosmoape.Parse(v)
	if err != nil {
		base.Fatalf("go: %s: %v", CosmoPlatformsEnv, err)
	}
	return set, true
}

// CosmoPlatforms returns the effective platform selection, the value
// `go env GOCOSMOPLATFORMS` reports. Consumers probe it to tell a toolchain
// that honors the selection from one that ignores it.
func CosmoPlatforms() string {
	set, _ := cosmoPlatformSpec()
	return set.String()
}

// cosmoPlatformArches returns the payload architectures the selection needs,
// or nil when GOCOSMOPLATFORMS is unset. A selection the current build
// cannot satisfy ends the build rather than quietly producing a binary for
// a different architecture than the caller asked to run on.
func cosmoPlatformArches() []string {
	set, explicit := cosmoPlatformSpec()
	if !explicit {
		return nil
	}
	arches := set.Arches()
	if !slices.Contains(arches, cfg.Goarch) {
		base.Fatalf("go: %s=%s boots the %s payload, but GOARCH=%s; rerun with GOARCH=%s",
			CosmoPlatformsEnv, set, strings.Join(arches, " and "), cfg.Goarch, arches[0])
	}
	if len(arches) > 1 && !cosmoFatEnv() {
		base.Fatalf("go: GOCOSMOFAT=0 conflicts with %s=%s, which needs both an amd64 and an arm64 payload",
			CosmoPlatformsEnv, set)
	}
	return arches
}

// cosmoFatEnv reports the GOCOSMOFAT setting: fat builds unless it is
// explicitly turned off.
func cosmoFatEnv() bool {
	switch os.Getenv("GOCOSMOFAT") {
	case "0", "off":
		return false
	}
	return true
}
