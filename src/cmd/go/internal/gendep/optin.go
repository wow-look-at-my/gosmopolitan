// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"cmd/go/internal/base"
)

// OrgPrefix names the module path prefix of this org. A module under it is
// written beside this toolchain and follows a branch, so its generators are as
// much this build's own as the fork's.
const OrgPrefix = "github.com/wow-look-at-my/"

// OptIn is the line a module outside the org writes in its own go.mod to ask
// this toolchain to run its generate directives.
const OptIn = "//go:gendep"

// Allowed reports whether the directives of the module at modroot, whose path
// is mod, may run.
//
// A directive is a command an author wrote, and completing a module runs it
// here without anybody reading it first. Inside the org that author is this
// fleet. Outside it, a build reaches hundreds of strangers, so each one runs
// nothing until it asks with a whole-line OptIn comment in its own go.mod.
//
// The module's own bytes decide, the way the directive count does. That keeps
// one module version meaning one thing to every machine reading the one cache
// key.
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
	open, err := os.Open(gomod)
	if err != nil {
		return false
	}
	defer open.Close()
	scan := bufio.NewScanner(open)
	scan.Buffer(nil, 1<<20)
	for scan.Scan() {
		if isOptIn(scan.Text()) {
			return true
		}
	}
	// A read that stopped short hides the rest of the file, and an opt-in under
	// the break then reads as a module that never asked.
	if err := scan.Err(); err != nil {
		base.Fatalf("go: reading %s: %v", gomod, err)
	}
	return false
}

// isOptIn reports whether line is the opt-in comment. The marker is the whole
// comment, so a go.mod that merely mentions it in a sentence asks for nothing.
func isOptIn(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), OptIn)
	return ok && strings.TrimSpace(rest) == ""
}
