// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// apeTargetEnv names a PRISTINE APE for TestAPEExec to run.
const (
	apeTargetEnv = "GO_TEST_APE_TARGET"
	apeArgsEnv   = "GO_TEST_APE_ARGS"
	apeWantEnv   = "GO_TEST_APE_WANT"
)

// TestAPEExec proves one cosmo binary can start another.
//
// A unix kernel refuses the MZqFpD header, so execve answers ENOEXEC
// unless the host carries a binfmt_misc entry for the magic - which
// needs root. A CI runner is not root. Without the /bin/sh retry in
// exec_cosmo.go every such start fails "exec format error". This takes
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

	// The same target by a relative name, from the directory holding it.
	cmd := exec.Command("./"+filepath.Base(target), args...)
	cmd.Dir = filepath.Dir(target)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the APE as %s from %s failed: %v\n%s", cmd.Path, cmd.Dir, err, out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("the APE printed %q, which does not contain %q", out, want)
	}

	// The same target by a BARE name, which is what net/http/cgi hands the kernel.
	base := filepath.Base(target)
	cmd = &exec.Cmd{
		Path: base,
		Args: append([]string{base}, args...),
		Dir:  filepath.Dir(target),
		Env:  []string{"PATH=/usr/bin:/bin"},
	}
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the APE as the bare name %s from %s failed: %v\n%s", base, cmd.Dir, err, out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("the APE printed %q, which does not contain %q", out, want)
	}
}

// startScript writes body to an executable file and starts it through
// os.StartProcess, which reaches the execve in exec_cosmo.go with no retry of
// its own. The script gets one argument, the path of a marker file.
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

// A file with the APE header still starts when the kernel refuses it: the
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
