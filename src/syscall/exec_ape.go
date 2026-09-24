// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo || darwin || linux

package syscall

import "unsafe"

// An APE is a shell script by construction, and its MZqFpD header is one
// no unix kernel execs. So execve answers ENOEXEC unless the host has a
// binfmt_misc entry for the magic, which needs root. /bin/sh reads that
// header. Every port this toolchain's own binaries are built for needs
// the retry: a linux or darwin go command starts the APEs it builds, and
// a cosmo binary starts other cosmo binaries.

// apeShellPath is the interpreter, as a NUL-terminated C string.
var apeShellPath = [...]byte{'/', 'b', 'i', 'n', '/', 's', 'h', 0}

// execAPEFallback retries an execve that answered ENOEXEC by handing
// the target to /bin/sh. Exec replaces this process, so a success never
// returns.
func execAPEFallback(argv0 *byte, argv, envv []*byte, err error) error {
	if err != ENOEXEC {
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
