// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"syscall"
	"testing"
)

// TestInotifyRecheckWaitsForTheLastHint holds what makes a dropped macOS
// fsevent visible. A recheck runs once, and only after its whole wait with no
// newer hint about the path.
func TestInotifyRecheckWaitsForTheLastHint(t *testing.T) {
	var test syscall.InotifyRechecksForTest
	runs := map[string]int{}
	again := func(path string) func() { return func() { runs[path]++ } }

	test.Recheck("/a", 1000, again("/a"))
	test.Recheck("/b", 1000, again("/b"))
	test.Age(600)
	test.Recheck("/a", 1000, again("/a"))
	test.Age(400)
	if runs["/a"] != 0 || runs["/b"] != 1 {
		t.Fatalf("after 1000ms: runs = %v, want /b once and /a not yet, because a later hint about /a restarted its wait", runs)
	}
	test.Age(599)
	if runs["/a"] != 0 {
		t.Fatalf("/a ran %d times 999ms after its last hint", runs["/a"])
	}
	test.Age(1)
	if runs["/a"] != 1 || runs["/b"] != 1 {
		t.Fatalf("runs = %v, want each path once", runs)
	}
	if pending := test.Pending(); pending != 0 {
		t.Fatalf("%d rechecks still wait after every one ran", pending)
	}
	test.Age(5000)
	if runs["/a"] != 1 || runs["/b"] != 1 {
		t.Fatalf("a recheck ran again with no new hint: runs = %v", runs)
	}
}
