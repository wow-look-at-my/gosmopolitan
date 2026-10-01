// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package embedstd

import (
	"strings"
	"testing"
)

func TestFileLinesNamesEachSourceFile(t *testing.T) {
	echo := strings.Join([]string{
		"mkdir -p $WORK/b001/",
		"cat >/tmp/importcfg << 'EOF' # internal",
		"packagefile errors=$WORK/b002/_pkg_.a",
		"EOF",
		"cd /goroot/src/fmt",
		`/goroot/pkg/tool/linux_amd64/compile -o $WORK/b001/_pkg_.a -trimpath "$WORK/b001=>" -p fmt -std -complete ./doc.go ./print.go`,
		"/usr/local/bin/go-toolchain tool asm -p internal/bytealg -trimpath $WORK -o $WORK/b003/cmp.o ./compare_amd64.s",
		"/usr/local/bin/go-toolchain tool link -o $WORK/b004/exe/a.out ./main.go",
		"",
	}, "\n")
	var out strings.Builder
	lines := &fileLines{target: "cosmo/amd64", out: &out}
	// A pipe hands the echo over in pieces that split lines.
	for _, piece := range []string{echo[:40], echo[40:200], echo[200:]} {
		if _, err := lines.Write([]byte(piece)); err != nil {
			t.Fatal(err)
		}
	}
	want := "embedstd: cosmo/amd64 fmt/doc.go\n" +
		"embedstd: cosmo/amd64 fmt/print.go\n" +
		"embedstd: cosmo/amd64 internal/bytealg/compare_amd64.s\n"
	if got := out.String(); got != want {
		t.Errorf("progress lines:\n%s\nwant:\n%s", got, want)
	}
	if got := lines.all.String(); got != echo {
		t.Errorf("the kept echo differs from what the go command wrote:\n%s", got)
	}
}
