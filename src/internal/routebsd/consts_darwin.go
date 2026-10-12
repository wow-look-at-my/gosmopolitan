// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package routebsd

import "syscall"

// On a darwin build these are what package syscall says. The cosmo build
// cannot say that; see consts_cosmo.go.
const (
	rtmNewAddr = syscall.RTM_NEWADDR
	rtmDelAddr = syscall.RTM_DELADDR
)
