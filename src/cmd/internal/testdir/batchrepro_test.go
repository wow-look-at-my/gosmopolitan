// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package testdir_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestBatchSourceIsTheSameEachRun: the batch executable must be the same bytes
// on every run. A wasm runtime keys its compiled module on those bytes, and the
// batch is the largest module the suite starts. A nonce in the dispatcher
// source, or a temporary directory left in its file names, makes each run
// compile the module again and adds one more copy to the runtime's cache.
func TestBatchSourceIsTheSameEachRun(t *testing.T) {
	render := func() string {
		var main strings.Builder
		if err := dispatcher.Execute(&main, dispatch{Programs: []program{{ID: "prog0"}, {ID: "prog1"}}}); err != nil {
			t.Fatal(err)
		}
		return main.String()
	}
	if first, second := render(), render(); first != second {
		t.Fatalf("two renders of the dispatcher differ:\n%s\n---\n%s", first, second)
	}

	objdir := filepath.Join("work", "b001")
	srcdir := filepath.Join("work", "testdir-batch-1234")
	unitHere := &unit{objdir: objdir + string(filepath.Separator)}
	bld := &builder{srcTrim: srcdir + "=>testdirbatch"}
	if trim, want := bld.trimpath(unitHere), objdir+"=>;"+srcdir+"=>testdirbatch"; trim != want {
		t.Fatalf("trimpath = %q, want %q: the objects first, then the source root", trim, want)
	}
	if trim := (&builder{}).trimpath(unitHere); trim != objdir+"=>" {
		t.Fatalf("trimpath with no source root = %q, want only the objects", trim)
	}
}
