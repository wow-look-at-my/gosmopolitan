// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package routebsd

// Apple's RTM_NEWADDR and RTM_DELADDR. They cannot come from package
// syscall on cosmo: netlink already owns those two names there, with the
// Linux numbers 20 and 21, and one binary carries both vocabularies
// because it boots on both kernels. Every other constant this package
// reads means the same thing in each, so only these two are indirected.
const (
	rtmNewAddr = 0xc
	rtmDelAddr = 0xd
)
