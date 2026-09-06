// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// Exports for darwinabi_cosmo_test.go.

var (
	XlatResourceForTest  = darwinXlatResource
	RlimitToLinuxForTest = darwinRlimitToLinux
	RlimitToAppleForTest = darwinRlimitToApple
	XlatUtimeNsecForTest = darwinXlatUtimeNsec
)

const (
	LinuxTIOCSCTTYForTest  = linuxTIOCSCTTY
	LinuxTIOCGPGRPForTest  = linuxTIOCGPGRP
	LinuxTIOCSPGRPForTest  = linuxTIOCSPGRP
	LinuxTIOCGWINSZForTest = linuxTIOCGWINSZ
	LinuxTIOCSWINSZForTest = linuxTIOCSWINSZ
	LinuxTIOCNOTTYForTest  = linuxTIOCNOTTY

	AppleTIOCSCTTYForTest  = appleTIOCSCTTY
	AppleTIOCGPGRPForTest  = appleTIOCGPGRP
	AppleTIOCSPGRPForTest  = appleTIOCSPGRP
	AppleTIOCGWINSZForTest = appleTIOCGWINSZ
	AppleTIOCSWINSZForTest = appleTIOCSWINSZ
	AppleTIOCNOTTYForTest  = appleTIOCNOTTY

	NiceBiasForTest       = darwinNiceBias
	LinuxRlimInfinityTest = linuxRLIM_INFINITY
	AppleRlimInfinityTest = appleRLIM_INFINITY
	LinuxUtimeNowForTest  = linuxUTIME_NOW
	LinuxUtimeOmitForTest = linuxUTIME_OMIT
	AppleUtimeNowForTest  = appleUTIME_NOW
	AppleUtimeOmitForTest = appleUTIME_OMIT
)
