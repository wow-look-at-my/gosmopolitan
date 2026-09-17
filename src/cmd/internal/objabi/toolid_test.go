// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objabi

import "testing"

// A linked tool's content ID is its own stamped ID. Any other tool answers
// the content half of the binary's build ID, and a binary the linker stamped
// nothing on answers nothing.
func TestToolContentIDPrefersTheLinkedID(test *testing.T) {
	test.Serial()
	wasIDs, wasBuildID := toolIDs, buildID
	test.Cleanup(func() { toolIDs, buildID = wasIDs, wasBuildID })

	toolIDs = "compile=cmp-id,go=go-id"
	buildID = "action-id/content-id"
	for _, row := range []struct{ name, want string }{
		{"go", "go-id"},
		{"compile", "cmp-id"},
		{"asm", "content-id"},
	} {
		if got := ToolContentID(row.name); got != row.want {
			test.Errorf("ToolContentID(%q) = %q, want %q", row.name, got, row.want)
		}
	}

	toolIDs, buildID = "", ""
	if got := ToolContentID("go"); got != "" {
		test.Errorf("ToolContentID(go) with nothing stamped = %q, want empty", got)
	}
}
