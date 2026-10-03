// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package gendep

import (
	"bytes"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"cmd/go/internal/base"
)

// OrgPrefix names the module path prefix of this org.
const OrgPrefix = "github.com/wow-look-at-my/"

// OptIn is the line a module outside the org writes in its own go.mod to ask this toolchain.
const OptIn = "//go:gendep"

// Allowed reports whether the directives of the module at modroot, whose path
// is mod, may run.
func Allowed(modroot, mod string) bool {
	if strings.HasPrefix(mod, OrgPrefix) {
		return true
	}
	return optedIn(modroot)
}

// optedIn reports whether the go.mod at modroot carries the opt-in line. A
// module that carries no go.mod carries no opt-in either.
func optedIn(modroot string) bool {
	gomod := filepath.Join(modroot, "go.mod")
	found := false
	err := fileLines(gomod, func(line []byte) bool {
		found = bytes.HasPrefix(bytes.TrimSpace(line), []byte(OptIn)) && isOptIn(string(line))
		return !found
	})
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	// A read that stops short hides the rest of the file, and an opt-in under
	// the break then reads as a module that never asked.
	if err != nil {
		base.Fatalf("go: reading %s: %v", gomod, err)
	}
	return found
}

// directiveLine answers line without its surrounding space, as a string, when
// it is a generate directive. The mapped line is copied only then.
func directiveLine(line []byte) (string, bool) {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte(generatePrefix)) {
		return "", false
	}
	rest := line[len(generatePrefix):]
	if len(rest) == 0 || (rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	return string(line), true
}

// isOptIn reports whether line is the opt-in comment.
func isOptIn(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), OptIn)
	return ok && strings.TrimSpace(rest) == ""
}
