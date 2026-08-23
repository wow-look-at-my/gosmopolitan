// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package main

import "runtime"

// A host-GOOS build of the probe (used for debugging) already names its
// host in runtime.GOOS.
func cosmoHostOS() string { return runtime.GOOS }
