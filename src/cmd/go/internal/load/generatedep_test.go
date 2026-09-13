// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"errors"
	"fmt"
	"testing"
)

// A host with no sandbox says nothing about the module being built, so the two
// kinds of failure have to stay apart. Reading them as one let a machine
// without bwrap record a permanent verdict against every module it touched,
// and installing bwrap afterwards could not clear it.
func TestSandboxUnavailableSeparatesHostFromModule(test *testing.T) {
	noSandbox := &sandboxUnavailableError{errors.New("bwrap not installed")}

	for _, row := range []struct {
		name string
		err  error
		host bool
	}{
		{"the host has no sandbox", noSandbox, true},
		{"a sandbox failure under a wrapper", fmt.Errorf("run: %w", noSandbox), true},
		{"the generator itself failed", errors.New("exit status 1"), false},
		{"the generator's input was missing", fmt.Errorf("open parser.c: %w", errors.New("no such file")), false},
		{"no failure at all", nil, false},
	} {
		test.Run(row.name, func(test *testing.T) {
			if got := sandboxUnavailable(row.err); got != row.host {
				test.Errorf("sandboxUnavailable(%v) = %v, want %v", row.err, got, row.host)
			}
		})
	}
}

// The message has to name the missing program, because the reader's next move
// is to install it.
func TestSandboxUnavailableKeepsItsMessage(test *testing.T) {
	inner := errors.New(`exec: "bwrap": executable file not found in $PATH`)
	wrapped := &sandboxUnavailableError{inner}

	if got := wrapped.Error(); got != inner.Error() {
		test.Errorf("Error() = %q, want %q", got, inner.Error())
	}
	if !errors.Is(wrapped, inner) {
		test.Error("the cause did not survive wrapping")
	}
}
