// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "unsafe"

// An APE is a shell script by construction. Its first bytes are the
// MZqFpD header, which no unix kernel will exec, so execve answers
// ENOEXEC unless the host carries a binfmt_misc entry for the magic -
// which needs root, and a CI runner is not root. /bin/sh is what reads
// that header, and misc/cosmo's exec wrappers are the same fallback
// applied from outside. Without this, one cosmo binary cannot start
// another: every re-exec of a test binary fails "exec format error".
var apeMagic = [8]byte{'M', 'Z', 'q', 'F', 'p', 'D', '=', '\''}

// apeShellPath is the interpreter, as a NUL-terminated C string.
var apeShellPath = [...]byte{'/', 'b', 'i', 'n', '/', 's', 'h', 0}

// isAPEPath reports whether the NUL-terminated path names a file whose
// first bytes are the APE magic. It must run BEFORE the fork: it opens
// and reads, and the child may do neither before its execve.
func isAPEPath(path *byte) bool {
	dirfd := _AT_FDCWD
	fd, _, e := RawSyscall6(SYS_OPENAT, uintptr(dirfd),
		uintptr(unsafe.Pointer(path)), uintptr(O_RDONLY|O_CLOEXEC), 0, 0, 0)
	if e != 0 {
		return false
	}
	var hdr [len(apeMagic)]byte
	n, _, e := RawSyscall(SYS_READ, fd, uintptr(unsafe.Pointer(&hdr[0])), uintptr(len(hdr)))
	RawSyscall(SYS_CLOSE, fd, 0, 0)
	if e != 0 || int(n) != len(hdr) {
		return false
	}
	return hdr == apeMagic
}

// execAPEFallback retries an execve that answered ENOEXEC by handing
// the target to /bin/sh, when the target is an APE. Exec replaces this
// process, so a success never returns.
func execAPEFallback(argv0 *byte, argv, envv []*byte, err error) error {
	if err != ENOEXEC || !isAPEPath(argv0) {
		return err
	}
	sh := apeShellArgv(argv0, argv)
	_, _, e := RawSyscall(SYS_EXECVE,
		uintptr(unsafe.Pointer(&apeShellPath[0])),
		uintptr(unsafe.Pointer(&sh[0])),
		uintptr(unsafe.Pointer(&envv[0])))
	return e
}

// apeShellArgv builds the /bin/sh form of a command: the interpreter,
// then the script, then the caller's own arguments. argv is the
// NUL-terminated argv execve takes, so argv[1:] carries the arguments
// and the terminator both. The result is allocated here, in the parent,
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
