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

// Linked reports whether this binary carries the tool called name.
func Linked(name string) bool {
	_, found := tools[name]
	return found
}

// Run runs the linked tool called name in this process, with args, the
// command line after the tool's own name, and answers its exit status. It
// reports false when no tool of that name is linked in, having run nothing.
//
// The tool takes over the process for the duration: it reads and writes this
// process's standard files, parses args on a flag set of its own, and exits
// the process itself on failure, exactly as it does when the go command
// starts it as a separate program. So a caller runs one tool and does no
// further work of its own afterwards.
func Run(name string, args []string) (code int, ran bool) {
	run, found := tools[name]
	if !found {
		return 0, false
	}
	return run(args), true
}

// Dispatch runs the tool that argv names and reports whether one ran. The
// program's own base name selects a tool, which is how a pkg/tool link to
// this binary runs, and so does "tool <name>" as the first two arguments,
// which is the command line the go command starts a linked tool with.
func Dispatch(argv []string) (code int, ran bool) {
	if len(argv) == 0 {
		return 0, false
	}
	if code, ran := Run(ToolName(argv[0]), argv[1:]); ran {
		return code, true
	}
	if len(argv) >= 3 && argv[1] == "tool" {
		return Run(argv[2], argv[3:])
	}
	return 0, false
}

// ToolName is the tool a program started under the path argv0 asks for: its
// base name, without the executable suffix a host puts on it.
func ToolName(argv0 string) string {
	return strings.TrimSuffix(filepath.Base(argv0), ".exe")
}
