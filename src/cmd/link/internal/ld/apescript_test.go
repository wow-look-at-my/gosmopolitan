// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"testing"
)

// The dispatch script runs from apeScriptOffset up to the Mach-O header, and
// overrunning that is a hard link failure with no other symptom. It had ONE
// byte spare, which a seven-byte edit to a path inside the script discovered
// by breaking the build. The ceiling is apeMachoOffset; this keeps a real
// margin so the next edit gets a message here instead, and so nobody has to
// find the budget the hard way twice.
func TestAPEScriptHasHeadroom(t *testing.T) {
	const minFree = 512

	amdElf, armElf := buildTestELFPair(t)
	amd, err := payloadFromELF(amdElf)
	if err != nil {
		t.Fatal(err)
	}
	arm, err := payloadFromELF(armElf)
	if err != nil {
		t.Fatal(err)
	}
	payloads := []*apePayload{amd, arm}
	layoutAPE(payloads)
	header := makeAPEHeaderForPayloads(payloads)

	// The script ends at its final "exit 1"; everything after is newline
	// padding up to the Mach-O header.
	window := header[apeScriptOffset:apeMachoOffset]
	end := bytes.LastIndex(window, []byte("\nexit 1\n"))
	if end < 0 {
		t.Fatal("no script terminator inside the script region")
	}
	used := end + len("\nexit 1\n")
	free := len(window) - used
	t.Logf("script uses %d of %d bytes (%d free)", used, len(window), free)
	if free < minFree {
		t.Errorf("only %d bytes left in the script region (%d used of %d); raise apeMachoOffset rather than shrinking the script into a corner",
			free, used, len(window))
	}
}
