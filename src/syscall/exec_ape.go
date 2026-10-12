// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || darwin || linux

package syscall

import "unsafe"

// execAPEFallback retries an execve that answered ENOEXEC by handing the
// target to /bin/sh. Exec replaces this process, so a success never returns.
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
