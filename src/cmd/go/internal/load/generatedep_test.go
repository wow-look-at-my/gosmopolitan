// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package load

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A host with no sandbox says nothing about the module being built, so the two
// kinds of failure have to stay apart. Reading them as one is what let a
// machine without bwrap record a permanent verdict against every module it
// touched, which installing bwrap then could not clear.
func TestAMissingSandboxIsNotAVerdictOnTheModule(test *testing.T) {
	missing := &sandboxUnavailableError{errors.New("bwrap not installed")}
	wrapped := fmt.Errorf("generating grammars/bash: %w", missing)

	var found *sandboxUnavailableError
	assert.True(test, errors.As(wrapped, &found), "a wrapped sandbox failure must still be recognisable")
	assert.Equal(test, "bwrap not installed", found.Error())

	generatorFailed := fmt.Errorf("exit status 1: parser.c: no such file")
	assert.False(test, errors.As(generatorFailed, &found), "a generator's own failure is not a sandbox failure")
}

// A recorded failure is read back on every later build, so only a verdict about
// the module's own bytes may be written down.
func TestOnlyAModuleVerdictIsRecorded(test *testing.T) {
	for _, row := range []struct {
		name    string
		err     error
		records bool
	}{
		{"the generator itself failed", errors.New("exit status 1"), true},
		{"the host has no sandbox", &sandboxUnavailableError{errors.New("bwrap not installed")}, false},
		{"a sandbox failure under a wrapper", fmt.Errorf("run: %w", &sandboxUnavailableError{errors.New("bwrap not installed")}), false},
	} {
		test.Run(row.name, func(test *testing.T) {
			failed := filepath.Join(test.TempDir(), "mod.failed")

			var unavailable *sandboxUnavailableError
			if !errors.As(row.err, &unavailable) {
				require.NoError(test, os.WriteFile(failed, []byte(row.err.Error()), 0o666))
			}

			_, err := os.Stat(failed)
			assert.Equal(test, row.records, err == nil, "whether the failure was recorded")
		})
	}
}
