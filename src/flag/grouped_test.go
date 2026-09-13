// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file is package flag_test, not package flag: testing imports flag, so an
// internal test file importing testing closes an import cycle.
package flag_test

import (
	. "flag"
	"testing"
)

// markGrouped makes this process look like a binary the linker built from
// several packages' tests, and gives the caller a fresh CommandLine to
// register into. Both are process-wide, so the caller runs alone.
func markGrouped(test *testing.T) {
	test.Helper()
	test.Serial()
	test.Cleanup(MarkGrouped())

	was := CommandLine
	ResetForTesting(nil)
	test.Cleanup(func() { CommandLine = was })
}

// Two members of one test binary declaring the same flag name is ordinary, and
// the argument has to reach both: the parser sees one flag, and whichever
// member's tests run has to read what the caller passed.
func TestASharedFlagNameReachesEveryMember(test *testing.T) {
	markGrouped(test)

	var one, two bool
	CommandLine.BoolVar(&one, "update", false, "first member")
	CommandLine.BoolVar(&two, "update", false, "second member")

	if err := CommandLine.Parse([]string{"-update"}); err != nil {
		test.Fatalf("parsing -update: %v", err)
	}
	if !one || !two {
		test.Errorf("-update reached one=%v two=%v, want both true", one, two)
	}
}

// The parser asks whether a flag is boolean to decide if the next argument
// belongs to it. Answering through the fan has to give the same answer the
// first registration would.
func TestASharedBoolFlagStillConsumesNoArgument(test *testing.T) {
	markGrouped(test)

	var one, two bool
	CommandLine.BoolVar(&one, "debug", false, "first member")
	CommandLine.BoolVar(&two, "debug", false, "second member")

	if err := CommandLine.Parse([]string{"-debug", "keep"}); err != nil {
		test.Fatalf("parsing -debug: %v", err)
	}
	if got := CommandLine.Args(); len(got) != 1 || got[0] != "keep" {
		test.Errorf("left %q, want [keep]: the bool flag ate its neighbour", got)
	}
}

// Only CommandLine collects several members' flags. On any other set a
// repeated name is what it always was, grouped binary or not: two declarations
// of one flag, which is a bug in the program.
func TestARepeatedNameStillPanicsOnAnOrdinarySet(test *testing.T) {
	test.Serial()
	test.Cleanup(MarkGrouped())

	defer func() {
		if recover() == nil {
			test.Error("redefining a flag on a plain FlagSet did not panic")
		}
	}()

	set := NewFlagSet("ordinary", ContinueOnError)
	var one, two bool
	set.BoolVar(&one, "update", false, "first")
	set.BoolVar(&two, "update", false, "second")
}
