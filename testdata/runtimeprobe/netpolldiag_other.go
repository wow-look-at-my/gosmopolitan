// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package main

// Non-cosmo builds (e.g. host-GOOS builds of the probe for debugging)
// have no cosmo runtime to interrogate.
func printNetpollDiag(tag string) {}
