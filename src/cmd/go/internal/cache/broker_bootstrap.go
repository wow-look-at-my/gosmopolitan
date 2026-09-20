// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap

package cache

// The broker serves the cache over a socket, and go_bootstrap may not depend
// on net. It has nothing to serve either: go_bootstrap builds this toolchain
// once, from source already on disk, and starts no go command of its own.

// dialBroker finds no owner to ask in a bootstrap toolchain.
func dialBroker() Cache { return nil }

// startBroker serves nothing in a bootstrap toolchain.
func startBroker(Cache, string) {}
