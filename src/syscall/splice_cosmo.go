// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

// Splice exists here for source compatibility. A package selects a file by
// the _linux name suffix, which cosmo keeps, and then calls syscall.Splice --
// the syscall_linux files that declare it are excluded by the cosmo tag, so
// such a package does not compile at all without this declaration. That is
// what stops github.com/hanwen/go-fuse from building for cosmo.
//
// Cosmopolitan has no splice: macOS and Windows have no counterpart, and
// internal/poll's cosmo build already answers "not supported" for the same
// reason. The call therefore fails with ENOSYS rather than reporting a copy
// that never happened, and a caller takes its ordinary read/write path. Go
// callers that probe splice at startup (go-fuse does) see the probe fail and
// stay on that path for the life of the process.
func Splice(rfd int, roff *int64, wfd int, woff *int64, len int, flags int) (n int64, err error) {
	return 0, ENOSYS
}
