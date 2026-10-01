// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package build

import (
	"os"
	"os/exec"
	"strings"
)

// cosmoImpliedTag reports whether a cosmo build always sets the tag. netgo
// and osusergo keep net and os/user in pure Go when cgo is on.
func cosmoImpliedTag(name string) bool {
	return name == "netgo" || name == "osusergo"
}

// cosmoCompilerFound reports whether the C compiler of a cosmo target is on PATH.
// It follows cmd/go: CC wins, then CC_FOR_cosmo_<arch>, then the cosmocc driver.
func cosmoCompilerFound(goarch string) bool {
	cc := os.Getenv("CC")
	if cc == "" {
		cc = os.Getenv("CC_FOR_cosmo_" + goarch)
	}
	if cc == "" {
		switch goarch {
		case "amd64":
			cc = "x86_64-unknown-cosmo-cc"
		case "arm64":
			cc = "aarch64-unknown-cosmo-cc"
		default:
			return false
		}
	}
	fields := strings.Fields(cc)
	if len(fields) == 0 {
		return false
	}
	_, err := exec.LookPath(fields[0])
	return err == nil
}
