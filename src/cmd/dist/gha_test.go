// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnnotateWarnings(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "src", "cmd", "dist")
	source := filepath.Join(workspace, "src", "internal", "lazyregexp", "lazyre.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	out := []byte("../../internal/lazyregexp/lazyre.go:30: performance warning: the pattern of regexp.MustCompile is not constant, so it compiles at run time\n" +
		"main.go:3:11: undefined: y\n")

	t.Setenv("GITHUB_ACTIONS", "")
	if got := string(annotateWarnings(out, dir)); got != string(out) {
		t.Errorf("outside GitHub Actions the output changed:\n%s", got)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", workspace)
	want := "::warning file=src/internal/lazyregexp/lazyre.go,line=30::performance warning: the pattern of regexp.MustCompile is not constant, so it compiles at run time\n" +
		"main.go:3:11: undefined: y\n"
	if got := string(annotateWarnings(out, dir)); got != want {
		t.Errorf("annotateWarnings:\ngot  %q\nwant %q", got, want)
	}
}
