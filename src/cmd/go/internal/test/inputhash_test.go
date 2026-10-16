// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scratchElsewhere moves the temporary directory away from dir, so the files
// a test writes there count as inputs and not as scratch.
func scratchElsewhere(t testing.TB, dir string) {
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, filepath.Join(dir, "scratch"))
	}
}

// TestRepeatedInputsKeepTheirLines: a file the log opens or stats more than
// once is hashed once, and each of its lines still goes into the ID, with the
// hash a direct hashOpen or hashStat gives. An ID from before the memo is the
// same ID now.
func TestRepeatedInputsKeepTheirLines(t *testing.T) {
	dir := t.TempDir()
	scratchElsewhere(t, dir)
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte(file), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	ops := []string{"open " + first, "stat " + second, "open " + first, "open " + second, "stat " + second, "open " + first}
	log := []byte("# test log\n" + strings.Join(ops, "\n") + "\n")
	_, lines, err := computeTestInputsID(inputsAction(dir), log)
	if err != nil {
		t.Fatal(err)
	}

	var want strings.Builder
	for _, op := range ops {
		kind, name, _ := strings.Cut(op, " ")
		if kind == "stat" {
			fmt.Fprintf(&want, "stat %s %x\n", name, hashStat(name))
			continue
		}
		id, err := hashOpen(name)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&want, "open %s %x\n", name, id)
	}
	got := string(lines)
	if !strings.HasSuffix(got, want.String()) {
		t.Fatalf("the file lines are not the ones each hash gives on its own\ngot:\n%s\nwant them to end:\n%s", got, want.String())
	}
}

// BenchmarkInputsOfATreeReadManyTimes is the log of a test that type-checks a
// source tree once per platform, as cmd/api does.
func BenchmarkInputsOfATreeReadManyTimes(b *testing.B) {
	tree := filepath.Join(runtime.GOROOT(), "src", "go")
	var files []string
	err := filepath.WalkDir(tree, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		b.Fatal(err)
	}
	scratchElsewhere(b, b.TempDir())
	var log strings.Builder
	log.WriteString("# test log\n")
	for range 20 {
		for _, file := range files {
			fmt.Fprintf(&log, "open %s\n", file)
		}
	}
	data := []byte(log.String())
	action := inputsAction(tree)
	b.ResetTimer()
	for range b.N {
		if _, _, err := computeTestInputsID(action, data); err != nil {
			b.Fatal(err)
		}
	}
}
