// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/cosmoape"
	"cmd/internal/sys"
	"slices"
	"strings"
)

// apeArchName is the GOARCH spelling of a payload architecture.
func apeArchName(a sys.ArchFamily) string {
	switch a {
	case sys.AMD64:
		return "amd64"
	case sys.ARM64:
		return "arm64"
	}
	Exitf("APE: unsupported payload architecture %v", a)
	return ""
}

// apePlatforms returns the host platforms the APE being written boots on.
//
// Without -apeplatforms the answer is descriptive: every platform the
// payloads on hand can serve. With -apeplatforms it is a requirement, so a
// selected platform whose payload is absent, or a payload no selected
// platform boots, ends the link. Either way the boot mechanisms
// makeAPEHeaderForPayloads emits are exactly the ones these platforms need.
func apePlatforms(payloads []*apePayload) cosmoape.Set {
	arches := make([]string, 0, len(payloads))
	for _, p := range payloads {
		arches = append(arches, apeArchName(p.arch))
	}
	if *flagApePlatforms == "" {
		return cosmoape.Default().RestrictToArches(arches)
	}
	set, err := cosmoape.Parse(*flagApePlatforms)
	if err != nil {
		Exitf("-apeplatforms: %v", err)
	}
	for _, p := range set.Platforms() {
		if !slices.Contains(arches, p.Arch) {
			Exitf("-apeplatforms: %s boots the %s payload, which this link has no input for (inputs: %s)", p, p.Arch, strings.Join(arches, ", "))
		}
	}
	for _, a := range arches {
		if !set.NeedsArch(a) {
			Exitf("-apeplatforms=%s: no selected platform boots the %s input; drop the input or select a platform that uses it", set, a)
		}
	}
	return set
}

// apeUnsupportedEcho is the shell statement a host the binary was not built
// for runs. Naming the platforms turns "exec format error" into an answer.
func apeUnsupportedEcho(plat cosmoape.Set) string {
	return "echo 'APE: unsupported host; this binary was built for " + plat.String() + "' >&2"
}
