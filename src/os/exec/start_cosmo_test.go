// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package exec_test

import (
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStartPristineAPE starts a freshly built APE straight from the file, with
// no wrapper and no binfmt registration: the kernel answers ENOEXEC on a posix
// host, and the file must still run.
func TestStartPristineAPE(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(src, []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() { fmt.Println(\"hello from\", os.Args[1]) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "hello.com")
	build := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", bin, src)
	build.Env = append(build.Environ(), "GOOS=cosmo")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the APE: %v\n%s", err, out)
	}
	head := make([]byte, 6)
	file, err := os.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Read(head); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if string(head) != "MZqFpD" {
		t.Fatalf("the build wrote a %q header, not an APE", head)
	}

	out, err := exec.Command(bin, "the pristine file").CombinedOutput()
	if err != nil {
		t.Fatalf("starting the pristine APE: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "hello from the pristine file" {
		t.Fatalf("the APE printed %q", got)
	}

	relative := exec.Command("./hello.com", "a relative path")
	relative.Dir = dir
	out, err = relative.CombinedOutput()
	if err != nil {
		t.Fatalf("starting the APE by a path relative to Dir: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "hello from a relative path" {
		t.Fatalf("the APE printed %q", got)
	}
}
