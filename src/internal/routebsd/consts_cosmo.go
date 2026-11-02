// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// Apple's RTM_NEWADDR and RTM_DELADDR.

package routebsd

// Apple's RTM_NEWADDR and RTM_DELADDR.
const (
	rtmNewAddr = 0xc
	rtmDelAddr = 0xd
)
