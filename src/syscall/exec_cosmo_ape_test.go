// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// apeTargetEnv names a PRISTINE APE for TestAPEExec to run, plus the
// arguments and the output that prove it ran. dats/cosmo-tests.dats
// builds one and sets them.
//
// Pristine matters. On a Linux host an APE assimilates itself on first
// run, overwriting its own header with the native ELF one, so a binary
// that has run once execs directly and proves nothing. A macOS host
// never assimilates, which is why it fails there first.
const (
	apeTargetEnv = "GO_TEST_APE_TARGET"
	apeArgsEnv   = "GO_TEST_APE_ARGS"
	apeWantEnv   = "GO_TEST_APE_WANT"
)

// TestAPEExec proves one cosmo binary can start another.
//
// A unix kernel refuses the MZqFpD header, so execve answers ENOEXEC
// unless the host carries a binfmt_misc entry for the magic - which
// needs root, and a CI runner is not root. Without the /bin/sh retry in
// exec_cosmo.go every such start fails "exec format error", which takes
// down t.Fork, t.Setenv, t.Chdir and every test that runs a helper.
func TestAPEExec(t *testing.T) {
	target := os.Getenv(apeTargetEnv)
	if target == "" {
		t.Skip(apeTargetEnv + " names the APE to run; dats/cosmo-tests.dats sets it")
	}
	want := os.Getenv(apeWantEnv)
	if want == "" {
		t.Fatal(apeWantEnv + " must say what the target prints")
	}

	var args []string
	if a := os.Getenv(apeArgsEnv); a != "" {
		args = strings.Fields(a)
	}
	out, err := exec.Command(target, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("running the APE %s failed: %v\n%s", target, err, out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("the APE printed %q, which does not contain %q", out, want)
	}
}
