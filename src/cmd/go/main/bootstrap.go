// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build compiler_bootstrap

// The bootstrap toolchain: the compiler, linker, assembler and cgo in one
// binary, built by the bootstrap Go and run by cmd/dist under each tool's
// name. There is no go command of this tree yet, so it carries none.
package main

import (
	"fmt"
	"os"

	"cmd/go/internal/selftool"
)

func main() {
	code, ran := selftool.Dispatch(os.Args)
	if !ran {
		fmt.Fprintf(os.Stderr, "%s: the bootstrap toolchain links only %v\n", os.Args[0], selftool.Names())
		os.Exit(2)
	}
	os.Exit(code)
}
