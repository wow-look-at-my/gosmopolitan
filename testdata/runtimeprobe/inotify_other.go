// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cosmo

package main

// checkInotify covers the cosmo inotify emulation, which only a cosmo build carries.
func checkInotify() { ok("inotify", "skipped: not a cosmo build") }
