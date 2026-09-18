// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package selftool names the tools linked into this binary and runs the one
// a command line asks for.
package selftool

import (
	"path/filepath"
	"slices"
	"strings"
)

// Names lists the linked tools in order.
func Names() []string {
	return slices.Sorted(func(yield func(string) bool) {
		for name := range tools {
			if !yield(name) {
				return
			}
		}
	})
}

// Dispatch runs the tool that argv names and reports whether one ran. The
// program's own base name selects a tool, which is how a pkg/tool link to
// this binary runs, and so does "tool <name>" as the first two arguments,
// which is the command line the go command starts a linked tool with.
func Dispatch(argv []string) (code int, ran bool) {
	if len(argv) == 0 {
		return 0, false
	}
	name := strings.TrimSuffix(filepath.Base(argv[0]), ".exe")
	if run, found := tools[name]; found {
		return run(argv[1:]), true
	}
	if len(argv) >= 3 && argv[1] == "tool" {
		if run, found := tools[argv[2]]; found {
			return run(argv[3:]), true
		}
	}
	return 0, false
}
