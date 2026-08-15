// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package main

import "runtime"

// cosmoHostOS is the runtime's own record of the host, set by the APE
// entry stub before any Go code runs.
func cosmoHostOS() string { return runtime.CosmoHostOS() }
