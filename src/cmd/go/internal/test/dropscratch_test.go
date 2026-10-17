// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDropScratch: the saved testlog keeps no read of this run's temporary
// directory or the directories above it. A run on a machine with another
// temporary directory would hash those paths as files that are gone.
func TestDropScratch(t *testing.T) {
	tmp := filepath.Clean(os.TempDir())
	pkgDir := t.TempDir()
	source, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(source, "test.go")
	lines := []string{
		"getenv HOME",
		"chdir " + filepath.Join(tmp, "work"),
		"open relative.txt",
		"open " + kept,
		"stat " + tmp,
		"stat " + filepath.Dir(tmp),
		"stat " + filepath.Dir(tmp) + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(filepath.Dir(tmp)),
		"parse open " + filepath.Join(tmp, "work", "b.go"),
	}
	testlog := append([]byte(nil), testlogMagic...)
	testlog = append(testlog, strings.Join(lines, "\n")+"\n"...)

	got := string(dropScratch(pkgDir, testlog))
	want := string(testlogMagic) + "getenv HOME\nopen " + kept + "\n"
	if got != want {
		t.Fatalf("dropScratch kept\n%s\nwant\n%s", got, want)
	}
}
