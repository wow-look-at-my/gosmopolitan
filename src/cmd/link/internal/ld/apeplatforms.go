// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/cosmoape"
	"cmd/internal/sys"
	"encoding/binary"
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

// checkNTBootHead ends the link when an explicitly selected windows/amd64
// would be served by the do-nothing stub PE header the amd64 input carries.
// The stub maps none of the payload, so the binary would claim a platform
// on which it starts and immediately returns 0.
func checkNTBootHead(amd *apePayload) {
	if n := binary.LittleEndian.Uint16(amd.head[0x86:0x88]); n != peCosmoSections {
		Exitf("-apeplatforms selects %s, but the amd64 input's PE header has %d sections, want %d: it is the do-nothing stub, not an NT boot header",
			cosmoape.WindowsAMD64, n, peCosmoSections)
	}
}

// apeUnsupportedEcho is the shell statement a host the binary was not built
// for runs. Naming the platforms turns "exec format error" into an answer.
func apeUnsupportedEcho(plat cosmoape.Set) string {
	return "echo 'APE: unsupported host; this binary was built for " + plat.String() + "' >&2"
}
