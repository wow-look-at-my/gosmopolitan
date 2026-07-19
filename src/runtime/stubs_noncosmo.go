// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cosmo

package runtime

// cosmoStacksAreSystemAllocated is a stub for non-cosmo systems; see
// mStackIsSystemAllocated and the GOOS=cosmo implementation in os_cosmo.go.
func cosmoStacksAreSystemAllocated() bool {
	return false
}

// cosmoHostIsWindows is a stub for non-cosmo systems; see
// sysReserveAligned in mem.go and the GOOS=cosmo implementation in
// os_cosmo.go.
//
//go:nosplit
func cosmoHostIsWindows() bool {
	return false
}

// cosmoNTGoargs is a stub for non-cosmo systems; see goargs in
// runtime1.go and the GOOS=cosmo implementation in os_cosmo_nt.go.
func cosmoNTGoargs() bool {
	return false
}

// cosmoMstartm0 is a stub for non-cosmo systems; see mstartm0 in
// proc.go and the GOOS=cosmo implementation in os_cosmo.go.
func cosmoMstartm0() {
}
