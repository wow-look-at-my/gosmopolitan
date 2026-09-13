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

// Each backend has to REPORT a missing program as a host fact. The predicate
// above cannot catch a backend that forgets to say so: it only reads what it is
// handed. darwin forgot, and a mac without sandbox-exec would have written a
// permanent verdict against every module it touched.
func TestEveryBackendReportsAMissingProgramAsAHostFact(test *testing.T) {
	for _, row := range []struct {
		name  string
		build func(string, []string) ([]string, error)
	}{
		{"linux", bwrapArgv},
		{"darwin", seatbeltArgv},
	} {
		test.Run(row.name, func(test *testing.T) {
			test.Setenv("PATH", test.TempDir())

			_, err := row.build(test.TempDir(), []string{"go", "generate", "./..."})
			if err == nil {
				test.Fatal("a backend found its program on an empty PATH")
			}
			if !sandboxUnavailable(err) {
				test.Errorf("%v reads as a failure of the module, not of the host", err)
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

// A generator's verdict is written once and replayed by every later build, so
// what rides the error is all a reader ever sees. "exit status 1" on its own
// sent a session hunting a runtime panic whose cause was printed hours before,
// in a build nobody still had the log of.
func TestTailWriterKeepsTheEndAndBoundsWhatItKeeps(test *testing.T) {
	for _, row := range []struct {
		name   string
		limit  int
		writes []string
		want   string
	}{
		{"short output survives whole", 16, []string{"open parser.c"}, "open parser.c"},
		{"the end wins over the start", 8, []string{"0123456789abcdef"}, "89abcdef"},
		{"writes across calls still tail", 6, []string{"aaaa", "bbbb", "cccc"}, "bbcccc"},
		{"nothing written reads empty", 8, nil, ""},
	} {
		test.Run(row.name, func(test *testing.T) {
			sink := &tailWriter{limit: row.limit}
			for _, payload := range row.writes {
				num, err := sink.Write([]byte(payload))
				if err != nil {
					test.Fatalf("Write(%q) failed: %v", payload, err)
				}
				// A short count makes io.MultiWriter report a write error and
				// take the generator's output away entirely.
				if num != len(payload) {
					test.Fatalf("Write(%q) = %d, want %d", payload, num, len(payload))
				}
			}
			if got := sink.String(); got != row.want {
				test.Errorf("String() = %q, want %q", got, row.want)
			}
			if got := len(sink.String()); got > row.limit {
				test.Errorf("kept %d bytes, over the %d-byte limit", got, row.limit)
			}
		})
	}
}
