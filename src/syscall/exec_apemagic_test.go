// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo || darwin || linux

package syscall_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// startScript writes body to an executable file and starts it through
// os.StartProcess, which reaches the execve of the forked child with no retry
// of its own. The script gets one argument, the path of a marker file.
func startScript(t *testing.T, body string) (marker string, err error) {
	dir := t.TempDir()
	script := filepath.Join(dir, "script")
	marker = filepath.Join(dir, "marker")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	proc, err := os.StartProcess(script, []string{script, marker}, &os.ProcAttr{})
	if err != nil {
		return marker, err
	}
	state, err := proc.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Success() {
		t.Fatalf("%s exited with %v", script, state)
	}
	return marker, nil
}

// A file with an APE header still starts when the kernel refuses it: the
// child retries it under /bin/sh. The binfmt_misc entry that APE staging
// registers covers only "MZqFpD='", so the "jartsr" file always takes the retry.
func TestExecRetriesAPEMagicThroughShell(t *testing.T) {
	for _, magic := range []string{"MZqFpD", "jartsr"} {
		marker, err := startScript(t, magic+"='\n'\nprintf launched > \"$1\"\n")
		if err != nil {
			t.Fatalf("a file that starts with %s did not start: %v", magic, err)
		}
		got, err := os.ReadFile(marker)
		if err != nil || string(got) != "launched" {
			t.Fatalf("a file that starts with %s did not run as a shell script: %q, %v", magic, got, err)
		}
	}
}

// A file with no #! line and no APE header is refused with ENOEXEC, as on
// stock Linux. The shell never runs it.
func TestExecPlainFileReturnsENOEXEC(t *testing.T) {
	marker, err := startScript(t, "printf launched > \"$1\"\n")
	if !errors.Is(err, syscall.ENOEXEC) {
		t.Fatalf("starting a file with no #! line: err = %v, want ENOEXEC", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the shell ran a file with no #! line")
	}
}
