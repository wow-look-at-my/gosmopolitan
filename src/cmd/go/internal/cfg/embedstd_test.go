// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cfg

import (
	"errors"
	"strings"
	"testing"
)

// A go command that embeds its standard library carries one target set and no
// other. The message names the GOOS and GOARCH that asked for another, so the
// reader looks at those rather than at a binary they take to be broken.
func TestTargetMessageNamesTheTargetAsked(t *testing.T) {
	carried := []string{"cosmo/amd64", "cosmo/arm64"}
	msg := targetMessage(carried, "linux", "amd64", errors.New(`no embedded entry "manifest/linux_amd64"`))

	for _, want := range []string{
		"builds for cosmo/amd64 and cosmo/arm64",
		"GOOS=linux GOARCH=amd64 names linux/amd64",
		"Leave GOOS and GOARCH unset",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not say %q:\n%s", want, msg)
		}
	}
	// The blob's own wording says nothing a caller can act on.
	if strings.Contains(msg, "no embedded entry") {
		t.Errorf("message repeats the blob's wording instead of the cause:\n%s", msg)
	}
}

// A binary carrying nothing is a different fault, and saying a target is wrong
// would point at the wrong thing.
func TestTargetMessageWithNothingCarried(t *testing.T) {
	msg := targetMessage(nil, "cosmo", "amd64", errors.New("blob is empty"))

	if !strings.Contains(msg, "carries no standard library at all") {
		t.Errorf("message does not report an empty binary:\n%s", msg)
	}
	if strings.Contains(msg, "Leave GOOS and GOARCH unset") {
		t.Errorf("message blames GOOS and GOARCH for an empty binary:\n%s", msg)
	}
}
