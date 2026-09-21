// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gocmd

import (
	"os"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/selftool"
	"internal/cosmo/embedded"
)

// Run is the whole program for a binary that links the go command and its
// tools: argv names a linked tool, by the program's base name or as
// "tool <name>", and Run answers that tool's exit status; otherwise argv is a
// go command line, the linked tools start as "<self> tool <name>", and a
// standard library carried in this executable is the GOROOT. A go command
// line exits the process itself, so Run returns from it only for "go help".
func Run(argv []string) int {
	return RunAs(argv, nil)
}

// RunAs is Run for a host binary that reaches the go command by a command
// line of its own, goCommand, which the go command uses to start itself
// again. A nil goCommand is this executable.
func RunAs(argv []string, goCommand []string) int {
	if code, ran := selftool.Dispatch(argv); ran {
		return code
	}
	if exe, err := os.Executable(); err == nil {
		base.SetSelf(exe, selftool.Names())
		if len(goCommand) > 0 {
			base.SetGoCommand(goCommand)
		}
		// A carried standard library is the one this command builds against,
		// whatever GOROOT names: an outside tree cannot put its own sources
		// under this binary's archives. A tree of the same toolchain still
		// answers for what no blob carries, cmd among it.
		if embedded.Available() {
			cfg.UseEmbeddedStd(exe)
		}
		publishGoCommand()
	}
	os.Args = argv
	Main()
	return base.GetExitStatus()
}

// goCommandEnv is the variable that names this go command, one argv word
// per line, to every program it starts. A program built by this toolchain
// asks go/build about packages, and go/build answers through this go
// command when GOROOT holds none, never through whichever go a shell has
// first on PATH.
const goCommandEnv = "GOCOMMAND"

// publishGoCommand sets goCommandEnv for the processes this go command
// starts. It runs before Main captures the environment those processes get.
func publishGoCommand() {
	argv, err := base.GoCommand()
	if err != nil {
		return
	}
	os.Setenv(goCommandEnv, strings.Join(argv, "\n"))
}
