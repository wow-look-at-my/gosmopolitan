// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package main

import "os/exec"

// startYielding starts cmd. This system gives dist no way to lower a
// process's priority, so cmd runs at dist's own.
func startYielding(cmd *exec.Cmd) error {
	return cmd.Start()
}
