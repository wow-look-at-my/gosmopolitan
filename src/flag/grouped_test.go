// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flag

import "testing"

// markGrouped makes this process look like a binary the linker built from
// several packages' tests, for the length of one test.
func markGrouped(test *testing.T) {
	test.Helper()
	was := groupedTestBinary
	groupedTestBinary = "1"
	test.Cleanup(func() { groupedTestBinary = was })
}

// Two members of one test binary declaring the same flag name is ordinary, and
// the argument has to reach both: the parser sees one flag, and whichever
// member's tests run has to read what the caller passed.
func TestASharedFlagNameReachesEveryMember(test *testing.T) {
	markGrouped(test)

	set := NewFlagSet("grouped", ContinueOnError)
	var one, two bool
	set.BoolVar(&one, "update", false, "first member")
	set.BoolVar(&two, "update", false, "second member")

	if err := set.Parse([]string{"-update"}); err != nil {
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

	set := NewFlagSet("grouped", ContinueOnError)
	var one, two bool
	set.BoolVar(&one, "debug", false, "first member")
	set.BoolVar(&two, "debug", false, "second member")

	if err := set.Parse([]string{"-debug", "keep"}); err != nil {
		test.Fatalf("parsing -debug: %v", err)
	}
	if got := set.Args(); len(got) != 1 || got[0] != "keep" {
		test.Errorf("left %q, want [keep]: the bool flag ate its neighbour", got)
	}
}

// Outside a grouped binary a repeated name is what it always was: two
// declarations of one flag, which is a bug in the program.
func TestARepeatedNameStillPanicsInAnOrdinaryBinary(test *testing.T) {
	if grouped() {
		test.Skip("this binary is a grouped one, so the panic is deliberately gone")
	}

	defer func() {
		if recover() == nil {
			test.Error("redefining a flag did not panic outside a grouped binary")
		}
	}()

	set := NewFlagSet("ordinary", ContinueOnError)
	var one, two bool
	set.BoolVar(&one, "update", false, "first")
	set.BoolVar(&two, "update", false, "second")
}
