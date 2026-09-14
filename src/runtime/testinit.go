// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import _ "unsafe" // for go:linkname

// testInitUnit lists the initialization records the tests of one package run
// before they start: what that package's _test.go files declare, compiled with
// -testinit, and whatever only they import. Program startup initializes
// everything else.
type testInitUnit struct {
	unit  string
	tasks []*initTask
}

// testinittasks is filled in by the linker, for a test binary that holds the
// tests of several packages.
var testinittasks []testInitUnit

// testDeps_runTestInit runs the deferred test initialization of package unit.
//
//go:linkname testDeps_runTestInit testing/internal/testdeps.runTestInit
func testDeps_runTestInit(unit string) {
	for idx := range testinittasks {
		if testinittasks[idx].unit == unit {
			doInit(testinittasks[idx].tasks)
			return
		}
	}
}
