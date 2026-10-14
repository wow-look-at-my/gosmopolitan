// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo

package main

// checkFileMap covers the APE loader, which only a cosmo build boots through.
func checkFileMap() { ok("filemap", "skipped: not a cosmo build") }
