// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package work

import (
	"path/filepath"
	"testing"
)

func TestAnnotateWarnings(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "src", "cmd", "dist")
	out := "../../internal/lazyregexp/lazyre.go:30:15: performance warning: the pattern of regexp.MustCompile is not constant, so it compiles at run time\n" +
		"main.go:3:11: undefined: y\n"

	t.Setenv("GITHUB_ACTIONS", "")
	if got := annotateWarnings(out, dir); got != out {
		t.Errorf("outside GitHub Actions the output changed:\n%s", got)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", workspace)
	want := "::warning file=src/internal/lazyregexp/lazyre.go,line=30,col=15::performance warning: the pattern of regexp.MustCompile is not constant, so it compiles at run time\n" +
		"main.go:3:11: undefined: y\n"
	if got := annotateWarnings(out, dir); got != want {
		t.Errorf("annotateWarnings:\ngot  %q\nwant %q", got, want)
	}
}

func TestCmdErrorHasNoHeader(t *testing.T) {
	err := &cmdError{desc: "cmd/dist", text: "main.go:3:11: undefined: y\n", importPath: "cmd/dist"}
	if got := err.Error(); got != "main.go:3:11: undefined: y\n" {
		t.Errorf("cmdError.Error() = %q, want the tool output alone", got)
	}
}
