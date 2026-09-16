// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo || darwin || linux

package exec_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// An APE is a shell script with no #! line, so execve answers ENOEXEC
// and the start goes through /bin/sh. Any script without a #! line takes
// the same path, which is what this exercises.
func TestStartsAScriptWithoutAnInterpreterLine(t *testing.T) {
	script := filepath.Join(t.TempDir(), "ape-shaped")
	if err := os.WriteFile(script, []byte("echo ape-shaped \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(script, "arg1", "arg2").Output()
	if err != nil {
		t.Fatalf("start %s: %v", script, err)
	}
	if got, want := string(out), "ape-shaped arg1 arg2\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
