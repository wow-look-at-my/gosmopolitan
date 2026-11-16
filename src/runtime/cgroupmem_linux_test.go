// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build linux

package runtime_test

import (
	"fmt"
	"internal/cgrouptest"
	"internal/testenv"
	"testing"
)

// runInCgroup runs the testprog function name inside the cgroup at path.
func runInCgroup(t *testing.T, path, exe, name string, env ...string) (string, error) {
	t.Helper()
	cmd := testenv.CleanCmdEnv(cgrouptest.CommandInCgroup(path, exe, name))
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// An unset GOMEMLIMIT takes nine tenths of a v1 cgroup's memory limit.
func TestCgroupV1MemoryLimit(t *testing.T) {
	exe, err := buildTestProg(t, "testprog")
	if err != nil {
		t.Fatal(err)
	}

	const limit = 4 << 30
	path := cgrouptest.MemoryCgroupV1(t, limit)

	got, err := runInCgroup(t, path, exe, "PrintMemoryLimit")
	want := fmt.Sprintf("%d\n", int64(limit-limit/10))
	if err != nil || got != want {
		t.Fatalf("output got %q (%v) want %q", got, err, want)
	}
}
