// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package work

// lowerToolPriority leaves pid's priority alone: this go command has no
// per-process nice value to set here.
func lowerToolPriority(pid int) error {
	return nil
}
