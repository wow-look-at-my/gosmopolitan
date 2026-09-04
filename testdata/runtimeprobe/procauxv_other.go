// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package main

// Only an APE has to serve /proc/self/auxv for itself.
func checkProcAuxv() { ok("procauxv", "not cosmo") }
