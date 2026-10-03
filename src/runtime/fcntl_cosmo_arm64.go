// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package runtime

import "internal/runtime/syscall/cosmo"

// fcntl performs the fcntl syscall with Linux cmd/arg encodings.
//
// On Linux this is a direct syscall. On macOS the cosmo dispatcher's
// darwin slow path emulates it through the dlsym'd Apple libc fcntl,
// translating F_* commands and O_* status flags. It covers F_GETFD,
// F_SETFD, F_GETFL, F_SETFL, F_DUPFD and F_DUPFD_CLOEXEC; anything else
// is ENOSYS, as is the darwin path before osArchInit has resolved the
// libc symbols. The runtime's only pre-main caller is checkfds, which
// runs from schedinit, after osinit.
//
//go:nosplit
func fcntl(fd, cmd, arg int32) (ret int32, errno int32) {
	r, _, err := cosmo.Syscall6(cosmo.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg), 0, 0, 0)
	return int32(r), int32(err)
}
