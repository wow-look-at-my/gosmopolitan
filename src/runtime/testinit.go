// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import _ "unsafe" // for go:linkname

// testInitUnit lists the initialization records the tests of one package run
// before they start: the package, what its _test.go files declare, compiled
// with -testinit, and whatever only they import. Program startup initializes
// only what the generated main itself runs on.
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

// testDeps_setDefaultGODEBUG makes def the binary's default GODEBUG, as the
// linker would have written it into a binary holding one package's tests.
// It runs before that package initializes, and reaches every setting that
// $GODEBUG can change while the program runs. A setting read only at startup
// keeps the value the binary started with, so cmd/go gives a package that
// needs another startup value a binary of its own.
//
//go:linkname testDeps_setDefaultGODEBUG testing/internal/testdeps.setDefaultGODEBUG
func testDeps_setDefaultGODEBUG(def string) {
	if def == godebugDefault {
		return
	}
	godebugDefault = def
	godebugNotify(true)
}
