// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package base

import (
	"slices"
	"testing"
)

// RunAtExit runs each function in order a single time, and OnExit refuses from then on.
func TestRunAtExitRunsEachFunctionOnce(t *testing.T) {
	t.Fork() // the exit state is process-wide
	var ran []string
	AtExit(func() { ran = append(ran, "first") })
	AtExit(func() { ran = append(ran, "second") })
	RunAtExit()
	RunAtExit()
	if !slices.Equal(ran, []string{"first", "second"}) {
		t.Fatalf("RunAtExit ran %q, want each function once in order", ran)
	}
	if OnExit(func() { ran = append(ran, "late") }) {
		t.Fatal("OnExit accepted a function after the exit began; its caller would create state nothing removes")
	}
	RunAtExit()
	if len(ran) != 2 {
		t.Fatalf("a refused function ran: %q", ran)
	}
}
