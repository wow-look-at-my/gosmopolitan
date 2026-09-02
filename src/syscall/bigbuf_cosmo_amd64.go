// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package syscall

// The macOS-Intel syscall path is not brought up (see
// docs/PLATFORM-STATUS.md): its darwin dispatch still issues raw XNU
// instructions, which the kernel answers with SIGSYS. Reaching these
// conversions there is impossible, so they report the gap rather than
// pretending to a translation the port below them cannot deliver.

func darwinStatfsPath(path string, buf *Statfs_t) error { return ENOSYS }

func darwinStatfsFd(fd int, buf *Statfs_t) error { return ENOSYS }

func darwinUname(buf *Utsname) error { return ENOSYS }
