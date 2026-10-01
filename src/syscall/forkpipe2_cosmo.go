// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

// forkPipeIsAtomic reports whether forkExecPipe returns a pipe that is
// already close-on-exec, which decides whether two forks may overlap.
//
// One APE asks this of three hosts and gets two answers. Linux has
// pipe2 and answers yes. Apple has no pipe2, so the emulation opens the
// pipe and then sets the flag, and NT builds its own descriptors; on
// both, the two forks have to take turns.
func forkPipeIsAtomic() bool { return cosmoHostIsLinux() }
