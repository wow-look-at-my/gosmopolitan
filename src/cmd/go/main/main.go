// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !compiler_bootstrap

// The go command and the tools it builds with, in one binary. Invoked by a
// tool's name, or as "go tool <name>", it runs that tool; otherwise it is the
// go command, which starts each linked tool as "<self> tool <name>".
package main

import (
	"os"

	gocmd "cmd/go"
	"cmd/go/internal/base"
	"cmd/go/internal/selftool"
)

func main() {
	if code, ran := selftool.Dispatch(os.Args); ran {
		os.Exit(code)
	}
	if exe, err := os.Executable(); err == nil {
		base.SetSelf(exe, selftool.Names())
	}
	gocmd.Main()
}
