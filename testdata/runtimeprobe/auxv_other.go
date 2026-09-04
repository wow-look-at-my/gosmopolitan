// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package main

// checkAuxv covers a boot block only a cosmo build assembles. A native
// darwin or windows binary genuinely has no auxv, so the check would
// fail there for a reason it does not exist to report.
func checkAuxv() { ok("auxv", "skipped: not a cosmo build") }

// checkProcAuxv covers the /proc emulation only a cosmo build carries.
func checkProcAuxv() { ok("procauxv", "skipped: not a cosmo build") }
