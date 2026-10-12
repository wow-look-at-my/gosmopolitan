// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

// The NT process-control emulation's tables, so a test outside this package
// can hold them against the file they came from (os_cosmo_nt_ctrl_test.go).

package runtime

const (
	NtIdlePriorityClass        = ntIdlePriorityClass
	NtBelowNormalPriorityClass = ntBelowNormalPriorityClass
	NtNormalPriorityClass      = ntNormalPriorityClass
	NtAboveNormalPriorityClass = ntAboveNormalPriorityClass
	NtHighPriorityClass        = ntHighPriorityClass
	NtRealtimePriorityClass    = ntRealtimePriorityClass
)

func NtPriorityClass(nice int32) uintptr { return ntPriorityClass(nice) }

func NtNice(class uintptr) int32 { return ntNice(class) }

func NtStatusErrno(status int32) uintptr { return ntStatusErrno(status) }
