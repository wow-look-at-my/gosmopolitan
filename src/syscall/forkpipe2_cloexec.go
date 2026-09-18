// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (dragonfly || freebsd || linux || netbsd || openbsd || solaris) && !cosmo

package syscall

// forkExecPipe atomically opens a pipe with O_CLOEXEC set on both file
// descriptors.
func forkExecPipe(p []int) error {
	return Pipe2(p, O_CLOEXEC)
}
