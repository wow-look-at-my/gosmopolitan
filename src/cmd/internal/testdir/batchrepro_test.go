// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package testdir_test

import (
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

	bld := &builder{srcTrim: "/tmp/testdir-batch-1234=>testdirbatch"}
	trim := bld.trimpath(&unit{objdir: "/tmp/work/b001/"})
	if want := "/tmp/work/b001=>;/tmp/testdir-batch-1234=>testdirbatch"; trim != want {
		t.Fatalf("trimpath = %q, want %q: the objects first, then the source root", trim, want)
	}
	if trim := (&builder{}).trimpath(&unit{objdir: "/tmp/work/b001/"}); trim != "/tmp/work/b001=>" {
		t.Fatalf("trimpath with no source root = %q, want only the objects", trim)
	}
}
