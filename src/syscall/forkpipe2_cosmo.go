// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

// forkPipeIsAtomic reports whether forkExecPipe returns a pipe that is already close-on-exec.
func forkPipeIsAtomic() bool { return cosmoHostIsLinux() }
