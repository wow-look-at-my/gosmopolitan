// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package work

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
	out := "../../internal/lazyregexp/lazyre.go:30:15: performance warning: the pattern of regexp.MustCompile is not constant, so it compiles at run time\n" +
		"github.com/dustin/go-humanize@v1.0.1/si.go:54:35: performance warning: x\n" +
		"main.go:3:11: undefined: y\n"

	t.Setenv("GITHUB_ACTIONS", "")
	if got := annotateWarnings(out, dir); got != out {
		t.Errorf("outside GitHub Actions the output changed:\n%s", got)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", workspace)
	want := "::warning file=src/internal/lazyregexp/lazyre.go,line=30,col=15::performance warning: the pattern of regexp.MustCompile is not constant, so it compiles at run time\n" +
		"::warning file=github.com/dustin/go-humanize@v1.0.1/si.go,line=54,col=35::performance warning: x\n" +
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
