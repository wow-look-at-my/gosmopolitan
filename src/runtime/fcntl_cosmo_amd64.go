// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import "internal/runtime/syscall/cosmo"

//go:nosplit
func fcntl(fd, cmd, arg int32) (ret int32, errno int32) {
	r, _, err := cosmo.Syscall6(cosmo.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg), 0, 0, 0)
	return int32(r), int32(err)
}
