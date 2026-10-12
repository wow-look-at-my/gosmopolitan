// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package testlog

import "sync/atomic"

// UnitEnv names the package a test binary holding several packages' tests
// runs, for a copy of that binary started as a child process.
const (
	UnitEnv         = "GO_TEST_UNIT"
	UnitImplicitEnv = "GO_TEST_UNIT_IMPLICIT"
)

var unit atomic.Pointer[string]

// SetUnit records the package whose tests this process runs, in a binary
// holding several packages' tests. It must be called only once.
func SetUnit(path string) {
	if !unit.CompareAndSwap(nil, &path) {
		panic("testlog: SetUnit must be called only once")
	}
}

// Unit answers the package SetUnit recorded, or "" in any other process.
func Unit() string {
	path := unit.Load()
	if path == nil {
		return ""
	}
	return *path
}
