// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package main

// checkLoadSpan covers the APE loader, which only a cosmo build boots through.
func checkLoadSpan() { ok("loadspan", "skipped: not a cosmo build") }
