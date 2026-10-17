// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"internal/testenv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReportSlowActionsReadsTheGoCommandGraph reads a graph the go command
// wrote, so a change to that format fails here and not in a CI log.
func TestReportSlowActionsReadsTheGoCommandGraph(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/graph\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	graph := filepath.Join(dir, "actions.json")
	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-debug-actiongraph="+graph, "-o", filepath.Join(dir, "out.exe"), ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	var report strings.Builder
	reportSlowActions(&report, "graph.test", graph)
	lines := strings.Split(strings.TrimSpace(report.String()), "\n")
	if len(lines) != slowActionCount+1 {
		t.Fatalf("got %d lines, want a span line and %d actions (the runtime alone is more actions than that):\n%s", len(lines), slowActionCount, report.String())
	}
	if !strings.Contains(lines[0], " span graph.test ") || !strings.HasSuffix(lines[0], " compiles ran") {
		t.Fatalf("first line %q does not give the step's span and compile count", lines[0])
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "dist action ") || !strings.Contains(line, " graph.test ") {
			t.Fatalf("line %q does not name a timed action of the step", line)
		}
	}
}

// TestReportSlowActionsNamesAMissingGraph checks that a step whose graph is
// missing says so in its report.
func TestReportSlowActionsNamesAMissingGraph(t *testing.T) {
	var report strings.Builder
	reportSlowActions(&report, "gone.test", filepath.Join(t.TempDir(), "absent.json"))
	if !strings.HasPrefix(report.String(), "dist action ERROR gone.test: no action graph") {
		t.Fatalf("report = %q, want an ERROR line for the missing graph", report.String())
	}
}
