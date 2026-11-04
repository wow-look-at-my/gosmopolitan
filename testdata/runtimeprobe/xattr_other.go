// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo

package main

// checkXattr covers the cosmo xattr emulation, which only a cosmo build carries.
func checkXattr() { ok("xattr", "skipped: not a cosmo build") }
