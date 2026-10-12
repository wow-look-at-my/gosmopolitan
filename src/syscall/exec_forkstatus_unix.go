// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build unix && !cosmo

package syscall

// readForkExecStatus reads the child's exec status off the pipe.
func readForkExecStatus(fd int, p *byte, np int, pid int) (n int, err error) {
	return readlen(fd, p, np)
}
