// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || darwin || linux || openbsd

package syscall

// An APE is a shell script by construction, and its MZqFpD header is one no unix kernel execs.

// apeShellPath is the interpreter, as a NUL-terminated C string.
var apeShellPath = [...]byte{'/', 'b', 'i', 'n', '/', 's', 'h', 0}

// apeMagics are the first bytes of an APE: the "MZqFpD" header the linker
// writes, and the "jartsr" header of an APE that does not rewrite itself.
var apeMagics = [...]string{"MZqFpD", "jartsr"}

// apeHeadSize is the length of each entry of apeMagics.
const apeHeadSize = 6

// isAPEHead reports whether head is an APE header. A forked child calls it.
//
//go:nosplit
//go:norace
func isAPEHead(head *[apeHeadSize]byte) bool {
	for _, magic := range apeMagics {
		same := true
		for idx := range head {
			if head[idx] != magic[idx] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// apeShellArgv builds the /bin/sh form of a command: the interpreter, then
// the script, then the caller's own arguments. argv is the NUL-terminated
// argv execve takes.
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
