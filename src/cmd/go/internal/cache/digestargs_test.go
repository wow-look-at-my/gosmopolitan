// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package cache

import (
	"path/filepath"
	"testing"
)

// TestDigestArgsIgnoresScratchPaths: two runs that differ only in their scratch
// directory name the same command, and so share one build manifest.
func TestDigestArgsIgnoresScratchPaths(t *testing.T) {
	tmp := t.TempDir()
	wd := filepath.Join(string(filepath.Separator), "src")
	first := filepath.Join(tmp, "go-tool-dist-111")
	second := filepath.Join(tmp, "go-tool-dist-222")
	runOne := []string{"test", "-o", filepath.Join(first, "std.test"), "-debug-actiongraph=" + filepath.Join(first, "std.test.actions.json"), "std"}
	runTwo := []string{"test", "-o", filepath.Join(second, "std.test"), "-debug-actiongraph=" + filepath.Join(second, "std.test.actions.json"), "std"}
	if digestArgs(wd, runOne, tmp) != digestArgs(wd, runTwo, tmp) {
		t.Fatal("two runs that differ only in their scratch paths digest apart")
	}

	other := []string{"test", "-o", filepath.Join(first, "std.test"), "-debug-actiongraph=" + filepath.Join(first, "std.test.actions.json"), "cmd"}
	if digestArgs(wd, runOne, tmp) == digestArgs(wd, other, tmp) {
		t.Fatal("two commands that build different packages digest the same")
	}
	elsewhere := []string{"test", "-o", filepath.Join(wd, "out", "std.test"), "std"}
	if digestArgs(wd, elsewhere, tmp) == digestArgs(wd, []string{"test", "-o", filepath.Join(wd, "other", "std.test"), "std"}, tmp) {
		t.Fatal("output paths outside the scratch space digest the same")
	}
	if digestArgs(wd, runOne, tmp) == digestArgs(filepath.Join(wd, "cmd"), runOne, tmp) {
		t.Fatal("the same command in another directory digests the same")
	}
	install := []string{"install", "std", "cmd"}
	if digestArgs(first, install, tmp) != digestArgs(second, install, tmp) {
		t.Fatal("one command run in two scratch directories digests apart")
	}
}
