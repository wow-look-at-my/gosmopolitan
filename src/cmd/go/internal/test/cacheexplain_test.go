// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cmd/go/internal/load"
	"cmd/go/internal/work"
)

func inputsAction(dir string) *work.Action {
	return &work.Action{Package: &load.Package{PackagePublic: load.PackagePublic{Dir: dir, ImportPath: "example/pkg"}}}
}

// TestInputsKeyTheRuntimeEnvironment: the runtime reads GOMAXPROCS and the
// rest at start without a testlog line, so a run under another value is
// another run.
func TestInputsKeyTheRuntimeEnvironment(t *testing.T) {
	dir := t.TempDir()
	log := []byte("# test log\n")
	for _, name := range runtimeEnv {
		t.Setenv(name, "1")
		before, lines, err := computeTestInputsID(inputsAction(dir), log)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(lines), "env "+name+" ") {
			t.Errorf("the inputs of an empty log name no %s:\n%s", name, lines)
		}
		t.Setenv(name, "2")
		after, _, err := computeTestInputsID(inputsAction(dir), log)
		if err != nil {
			t.Fatal(err)
		}
		if before == after {
			t.Errorf("%s=1 and %s=2 key the same result", name, name)
		}
	}
}

// TestInputLinesNameWhatMoved: the lines computeTestInputsID answers are the
// ones explainInputsMiss compares, one name to one hash, so a changed file is
// the one name whose hash differs.
func TestInputLinesNameWhatMoved(t *testing.T) {
	dir := t.TempDir()
	// A file under the temporary directory is scratch and hashes to nothing,
	// so the temporary directory moves to one these files are not in.
	scratch := filepath.Join(dir, "scratch")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, scratch)
	}
	steady := filepath.Join(dir, "steady.txt")
	moving := filepath.Join(dir, "moving.txt")
	for _, file := range []string{steady, moving} {
		if err := os.WriteFile(file, []byte("one"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	log := []byte("# test log\nopen " + steady + "\nopen moving.txt\ngetenv HOME\n")
	_, before, err := computeTestInputsID(inputsAction(dir), log)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moving, []byte("two"), 0o666); err != nil {
		t.Fatal(err)
	}
	_, after, err := computeTestInputsID(inputsAction(dir), log)
	if err != nil {
		t.Fatal(err)
	}
	now := inputHashes(after)
	moved := movedInputs(inputHashes(before), now)
	if want := "input open " + moving + " changed"; len(moved) != 1 || moved[0] != want {
		t.Errorf("moved = %q, want only %q", moved, want)
	}
	if _, found := now["env HOME"]; !found {
		t.Errorf("the inputs name no HOME: %q", now)
	}
	if moved := movedInputs(now, now); moved == nil || len(moved) != 0 {
		t.Errorf("movedInputs of equal inputs = %#v, want an empty list", moved)
	}
}

// TestMovedInputsNamesNewAndGone: an input only now read is new, and one only
// read before is gone.
func TestMovedInputsNamesNewAndGone(t *testing.T) {
	before := map[string]string{"env GOFIPS140": "1", "open /a": "2"}
	after := map[string]string{"env GOFIPS140": "1", "open /b": "3"}
	want := []string{"input open /b is new", "input open /a is gone"}
	if moved := movedInputs(before, after); !slices.Equal(moved, want) {
		t.Errorf("moved = %q, want %q", moved, want)
	}
}

func TestBadTestlogNamesItsLine(t *testing.T) {
	_, _, err := computeTestInputsID(inputsAction(t.TempDir()), []byte("# test log\n\x00\x00open x\n"))
	if err == nil || !strings.Contains(err.Error(), `\x00\x00open x`) {
		t.Errorf("err = %v, want one naming the line", err)
	}
}

func TestIdentityWords(t *testing.T) {
	before := identityWords("bufio=AAA bytes=BBB main digest1 link1")
	after := identityWords("bufio=AAA bytes=CCC net=DDD main digest1 link2")
	for name, want := range map[string][2]string{
		"bufio": {"AAA", "AAA"},
		"bytes": {"BBB", "CCC"},
		"net":   {"", "DDD"},
		"#0":    {"main", "main"},
		"#1":    {"digest1", "digest1"},
		"#2":    {"link1", "link2"},
	} {
		if before[name] != want[0] || after[name] != want[1] {
			t.Errorf("%s: %q then %q, want %q then %q", name, before[name], after[name], want[0], want[1])
		}
	}
}
