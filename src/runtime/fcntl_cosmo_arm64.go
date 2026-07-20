// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import "internal/runtime/syscall/cosmo"

// fcntl performs the fcntl syscall with Linux cmd/arg encodings.
//
// On Linux hosts this is a direct syscall. On macOS the generic cosmo
// dispatcher's darwin slow path emulates fcntl through the dlsym'd Apple
// libc fcntl, translating F_* commands and O_* status flags, so the same
// call works there too (F_GETFD, F_SETFD, F_GETFL, F_SETFL, F_DUPFD and
// F_DUPFD_CLOEXEC; other commands fail with ENOSYS).
//
// Before osArchInit has resolved the libc symbols (i.e. before osinit),
// the darwin path fails with ENOSYS; the runtime's only pre-main caller,
// checkfds, runs from schedinit which is after osinit.
//
//go:nosplit
func fcntl(fd, cmd, arg int32) (ret int32, errno int32) {
	r, _, err := cosmo.Syscall6(cosmo.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg), 0, 0, 0)
	return int32(r), int32(err)
}
