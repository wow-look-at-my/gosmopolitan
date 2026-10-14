// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package work

import "testing"

// The ID read in-process must equal the ID parsed from the tool's -V=full
// line, for every shape of build ID. A difference makes the go command
// stale against itself.
func TestInProcessToolIDMatchesTheToolsLine(t *testing.T) {
	for _, buildID := range []string{"", "iHPvOU5HYdK8ZU6WhaV9", "action/content"} {
		line := "link version go1.27.0-cosmo buildID=" + buildID + "\n"
		want, ok := parseToolID("link", false, line)
		if !ok {
			t.Fatalf("parseToolID(%q) failed", line)
		}
		if got := toolIDFromField("buildID=" + buildID); got != want {
			t.Errorf("build ID %q: in-process %q, parsed from the line %q", buildID, got, want)
		}
	}
}
