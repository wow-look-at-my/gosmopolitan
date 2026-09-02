// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package syscall

// statfs and fstatfs are shared (bigbuf_cosmo.go): the amd64 emulation
// serves them through raw XNU statfs64/fstatfs64, with the same
// buffer-size guard the arm64 path applies.
//
// uname is not, and cannot be. XNU has no uname syscall - it is a libc
// function over sysctl - and the amd64 path dispatches by NUMBER, so
// there is nothing to dispatch to. arm64 escapes this only because it
// resolves Apple's libc uname by name through dlsym, a mechanism amd64
// does not have (the Syslib comes from the ARM64 APE loader).
//
// Reporting ENOSYS is the honest answer here. Synthesizing a plausible
// utsname from sysctl values would be inventing a system's identity
// rather than reading it.

func darwinUname(buf *Utsname) error { return ENOSYS }
