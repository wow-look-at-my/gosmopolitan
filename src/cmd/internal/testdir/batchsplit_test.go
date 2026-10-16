// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package testdir_test

import (
	"maps"
	"testing"
)

// TestEligibleRunHeader: the first line names the action. Most run programs
// have a blank line after it.
func TestEligibleRunHeader(t *testing.T) {
	cases := map[string]bool{
		"// run\n\n// Copyright.\n\npackage main\n\nfunc main() {\n}\n":    true,
		"// run\n// Copyright.\n\npackage main\n\nfunc main() {\n}\n":      true,
		"//run\n\npackage main\n\nfunc main() {\n}\n":                      true,
		"// skip\n// run\n\npackage main\n\nfunc main() {\n}\n":            false,
		"// runoutput\n\npackage main\n\nfunc main() {\n}\n":               false,
		"// run -gcflags=-l\n\npackage main\n\nfunc main() {\n}\n":         false,
		"// run\n\n//go:build amd64\n\npackage main\n\nfunc main() {\n}\n": false,
		"// compile\n\n// run\n\npackage main\n\nfunc main() {\n}\n":       false,
		"// run\n\npackage p\n\nfunc main() {\n}\n":                        false,
	}
	for src, want := range cases {
		if _, got := eligible(src); got != want {
			t.Errorf("eligible(%q) = %v, want %v", src, got, want)
		}
	}
}

// TestSplitBatch: a program counts as finished only when the next marker
// follows its output. A process that dies inside a program names it.
func TestSplitBatch(t *testing.T) {
	const nonce = "nonce"
	whole := "init noise\nnonce prog0\nzero\nnonce prog1\none\nnonce .\n"
	finished, started := splitBatch([]byte(whole), nonce)
	want := map[string][]byte{"prog0": []byte("zero\n"), "prog1": []byte("one\n")}
	if !equalOutputs(finished, want) || started != "" {
		t.Fatalf("whole run: finished %q, started %q; want %q and nothing started", finished, started, want)
	}

	died := "nonce prog0\nzero\nnonce prog1\npanic: half\n"
	finished, started = splitBatch([]byte(died), nonce)
	want = map[string][]byte{"prog0": []byte("zero\n")}
	if !equalOutputs(finished, want) || started != "prog1" {
		t.Fatalf("died in prog1: finished %q, started %q; want %q and prog1 started", finished, started, want)
	}

	finished, started = splitBatch([]byte("panic in init\n"), nonce)
	if len(finished) != 0 || started != "" {
		t.Fatalf("died before main: finished %q, started %q; want nothing", finished, started)
	}
}

func equalOutputs(got, want map[string][]byte) bool {
	return maps.EqualFunc(got, want, func(left, right []byte) bool { return string(left) == string(right) })
}
