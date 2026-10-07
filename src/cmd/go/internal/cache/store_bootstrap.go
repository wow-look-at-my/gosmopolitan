// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap

package cache

import "github.com/wow-look-at-my/go-s3-server/cacheclient/cachedisk"

// The store speaks HTTP and the broker speaks to a socket, and go_bootstrap may not depend on net.

// openCache answers the directory alone, with nothing under it.
func openCache(dir string) (Cache, error) { return cachedisk.Open(dir) }

// SetSharedModule has no store to name a module to.
func SetSharedModule(string) {}

// MissNotices is false: go_bootstrap builds the toolchain and runs no tests.
func MissNotices() bool { return false }

// MissNotice has no notice file to write to.
func MissNotice(string, ...any) {}
