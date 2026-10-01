// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix && !cosmo

package syscall

// readForkExecStatus reads the child's exec status off the pipe. The kernel
// closes the pipe when the child execs or exits, so the read ends on its own.
func readForkExecStatus(fd int, p *byte, np int, pid int) (n int, err error) {
	return readlen(fd, p, np)
}
