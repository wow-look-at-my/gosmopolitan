// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regexp

import (
	"reflect"
	"testing"
)

var variablePattern = []string{`a(?P<name>b+)c|d`}

// TestConstantPatternIsPrecompiled checks that the compiler built the
// Regexp of a constant pattern. Two calls share one program, which a
// run-time compile never does: it builds a new program on every call.
func TestConstantPatternIsPrecompiled(t *testing.T) {
	first := MustCompile(`a(?P<name>b+)c|d`)
	second := MustCompile(`a(?P<name>b+)c|d`)
	if first == second {
		t.Fatal("two MustCompile calls returned the same Regexp")
	}
	if first.prog != second.prog {
		t.Fatal("the constant pattern compiled at run time")
	}
	run := MustCompile(variablePattern[0])
	if run.prog == first.prog {
		t.Fatal("a variable pattern shares the precompiled program")
	}
	if !reflect.DeepEqual(*first, *run) {
		t.Fatal("the precompiled Regexp differs from the run-time one")
	}

	first.Longest()
	if second.longest {
		t.Error("Longest changed another Regexp")
	}
	first.SubexpNames()[1] = "changed"
	if second.SubexpNames()[1] != "name" {
		t.Error("SubexpNames is shared between Regexps")
	}

	posix, err := CompilePOSIX(`a|ab`)
	if err != nil || !posix.longest || posix.FindString("ab") != "ab" {
		t.Errorf("CompilePOSIX of a constant: %v, longest %v", err, posix.longest)
	}
	if _, err := Compile(`a(`); err == nil {
		t.Error("Compile of an invalid constant returned no error")
	}
}
