// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "unsafe"

// An APE is a shell script by construction, and its MZqFpD header is one
// no unix kernel execs. So execve answers ENOEXEC unless the host has a
// binfmt_misc entry for the magic, which needs root. /bin/sh reads that
// header. Without this, one cosmo binary cannot start another. The shell
// form itself is in exec_ape.go, which a darwin host needs too: this
// toolchain runs there natively and starts the APEs it builds.

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
