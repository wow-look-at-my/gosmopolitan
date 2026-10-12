// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package runtime

import _ "unsafe" // for go:linkname

// testInitUnit lists the initialization records the tests of one package run
// before they start.
type testInitUnit struct {
	unit  string
	tasks []*initTask
}

// testinittasks is filled in by the linker, for a test binary that holds the tests of several packages.
var testinittasks []testInitUnit

// testDeps_runTestInit runs the deferred test initialization of package unit.
//
//go:linkname testDeps_runTestInit testing/internal/testdeps.runTestInit
func testDeps_runTestInit(unit string) {
	if len(testinittasks) == 0 {
		return
	}
	for idx := range testinittasks {
		if testinittasks[idx].unit == unit {
			doInit(testinittasks[idx].tasks)
			break
		}
	}
	// runtime.main left the main goroutine locked to the main thread for this init, as it is for every other package's.
	unlockOSThread()
}

// testDeps_setDefaultGODEBUG makes def the binary's default GODEBUG.
//
//go:linkname testDeps_setDefaultGODEBUG testing/internal/testdeps.setDefaultGODEBUG
func testDeps_setDefaultGODEBUG(def string) {
	if def == godebugDefault {
		return
	}
	godebugDefault = def
	godebugNotify(true)
}
