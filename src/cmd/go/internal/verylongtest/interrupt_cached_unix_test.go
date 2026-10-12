// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package verylongtest

import (
	"bufio"
	"context"
	"internal/testenv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestTestInterruptBeforeAnyTestBinary is TestTestInterrupt with the timing
// pinned down. The first package's result comes from the cache, and the second
// package cannot finish building because its C compiler never returns. So when
// the first line arrives, 'go test' has printed a result and started no test
// binary. SIGINT to the process group at that point must still end in an exit
// status, not in the go command dying of the signal.
func TestTestInterruptBeforeAnyTestBinary(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	testenv.MustHaveCGO(t)
	testenv.MustHaveExecPath(t, "sh")

	gotool, err := testenv.GoTool()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/interrupt\n\ngo 1.27\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"b/b.go":      "package b\n\n// int b(void) { return 1; }\nimport \"C\"\n\nfunc B() int { return int(C.b()) }\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) { B() }\n",
		"cc.sh":       "#!/bin/sh\nexec sleep 3600\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o777); err != nil {
			t.Fatal(err)
		}
	}

	warm := exec.Command(gotool, "test", "./a")
	warm.Dir = dir
	if out, err := warm.CombinedOutput(); err != nil {
		t.Fatalf("go test ./a: %v\n%s", err, out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := testenv.CommandContext(t, ctx, gotool, "test", "./a", "./b")
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "CC="+filepath.Join(dir, "cc.sh"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("running %v", cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	r := bufio.NewReader(pipe)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the first line of 'go test': %v", err)
	}
	if !strings.Contains(line, "example.com/interrupt/a") || !strings.Contains(line, "(cached)") {
		t.Fatalf("first line is %q, want the cached result of example.com/interrupt/a", line)
	}

	cancel()

	stdout := new(strings.Builder)
	stdout.WriteString(line)
	io.Copy(stdout, r)
	t.Logf("stdout:\n%s", stdout)
	err = cmd.Wait()

	ee, _ := err.(*exec.ExitError)
	if ee == nil {
		t.Fatalf("'go test' finished with %v, want a nonzero exit status", err)
	}
	if !ee.Exited() {
		t.Fatalf("'go test' did not exit after interrupt: %v", err)
	}
}
