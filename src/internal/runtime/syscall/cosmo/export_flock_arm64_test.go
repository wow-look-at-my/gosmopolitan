// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

// Exports for flock_cosmo_arm64_test.go.

type (
	LinuxFlockForTest = linuxFlock
	AppleFlockForTest = appleFlock
)

var (
	FlockToAppleForTest   = flockToApple
	FlockFromAppleForTest = flockFromApple
)

const (
	LinuxF_GETLKForTest  = linuxF_GETLK
	LinuxF_SETLKForTest  = linuxF_SETLK
	LinuxF_SETLKWForTest = linuxF_SETLKW
	AppleF_GETLKForTest  = appleF_GETLK
	AppleF_SETLKForTest  = appleF_SETLK
	AppleF_SETLKWForTest = appleF_SETLKW

	LinuxF_RDLCKForTest = linuxF_RDLCK
	LinuxF_WRLCKForTest = linuxF_WRLCK
	LinuxF_UNLCKForTest = linuxF_UNLCK
	AppleF_RDLCKForTest = appleF_RDLCK
	AppleF_WRLCKForTest = appleF_WRLCK
	AppleF_UNLCKForTest = appleF_UNLCK
)
