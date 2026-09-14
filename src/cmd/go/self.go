// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gocmd

import (
	"os"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/selftool"
	"internal/cosmo/embedded"
)

// Run is the whole program for a binary that links the go command and its
// tools: argv names a linked tool, by the program's base name or as
// "tool <name>", and Run answers that tool's exit status; otherwise argv is a
// go command line, the linked tools start as "<self> tool <name>", and a
// standard library carried in this executable is the GOROOT when the
// environment names none. A go command line exits the process itself, so
// Run returns from it only for "go help".
func Run(argv []string) int {
	if code, ran := selftool.Dispatch(argv); ran {
		return code
	}
	if exe, err := os.Executable(); err == nil {
		base.SetSelf(exe, selftool.Names())
		if goroot := os.Getenv("GOROOT"); (goroot == "" || goroot == exe) && embedded.Available() {
			cfg.UseEmbeddedStd(exe)
		}
	}
	os.Args = argv
	Main()
	return base.GetExitStatus()
}
