// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo || darwin

package syscall

// apeShellPath is the interpreter, as a NUL-terminated C string.
var apeShellPath = [...]byte{'/', 'b', 'i', 'n', '/', 's', 'h', 0}

// apeShellArgv builds the /bin/sh form of a command: the interpreter,
// then the script, then the caller's own arguments. argv is the
// NUL-terminated argv execve takes, so argv[1:] carries the arguments
// and the terminator both. The result is allocated in the parent,
// because the child cannot allocate.
func apeShellArgv(argv0 *byte, argv []*byte) []*byte {
	sh := make([]*byte, 0, len(argv)+1)
	sh = append(sh, &apeShellPath[0], argv0)
	if len(argv) > 1 {
		sh = append(sh, argv[1:]...)
	} else {
		sh = append(sh, nil)
	}
	return sh
}
