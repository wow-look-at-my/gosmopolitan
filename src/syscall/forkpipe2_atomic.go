// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (dragonfly || freebsd || linux || netbsd || openbsd || solaris) && !cosmo

package syscall

// forkPipeIsAtomic reports whether forkExecPipe returns a pipe that is
// already close-on-exec. These systems have pipe2, so it does, and
// forks may overlap.
func forkPipeIsAtomic() bool { return true }
