// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap

package cache

// The shared cache tier speaks HTTP, and go_bootstrap may not depend on net.
// It is also the wrong thing to want here: go_bootstrap exists to build this
// toolchain once, from source that is already on disk.

// Shared reports whether a shared cache tier is configured. Never, here.
func Shared() bool { return false }

// newSharedCache has no tier to build in a bootstrap toolchain.
func newSharedCache(*DiskCache) Cache { return nil }
