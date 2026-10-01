// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package cfg

import (
	"os"
	"strings"

	"cmd/internal/pathcache"
)

// TargetCC is DefaultCC, but a cosmo target reads CC_FOR_cosmo_<arch> first.
// A fat build compiles C for each architecture, so one CC cannot serve it.
func TargetCC(goos, goarch string) string {
	if goos == "cosmo" {
		if override := os.Getenv("CC_FOR_cosmo_" + goarch); override != "" {
			return override
		}
	}
	return DefaultCC(goos, goarch)
}

// TargetCXX is the C++ counterpart of TargetCC.
func TargetCXX(goos, goarch string) string {
	if goos == "cosmo" {
		if override := os.Getenv("CXX_FOR_cosmo_" + goarch); override != "" {
			return override
		}
	}
	return DefaultCXX(goos, goarch)
}

// cosmoCgoDefault reports whether cgo is on by default for a cosmo target.
// It is on when the C compiler for that architecture is on PATH.
func cosmoCgoDefault(goarch string) bool {
	compiler := Getenv("CC")
	if compiler == "" {
		compiler = TargetCC("cosmo", goarch)
	}
	fields := strings.Fields(compiler)
	if len(fields) == 0 {
		return false
	}
	_, err := pathcache.LookPath(fields[0])
	return err == nil
}
