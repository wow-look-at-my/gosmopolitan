// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flags implements top-level flags and the usage message for the assembler.
package flags

import (
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Set is the assembler's command line, one set of its own so the assembler can
// be linked beside the other tools; cmd/asm installs it before Parse.
var Set = flag.NewFlagSet("asm", flag.ExitOnError)

var (
	Debug      = Set.Bool("debug", false, "dump instructions as they are parsed")
	OutputFile = Set.String("o", "", "output file; default foo.o for /a/b/c/foo.s as first argument")
	TrimPath   = Set.String("trimpath", "", "remove prefix from recorded source file paths")
	Shared     = Set.Bool("shared", false, "generate code that can be linked into a shared library")
	Dynlink    = Set.Bool("dynlink", false, "support references to Go symbols defined in other shared libraries")
	Linkshared = Set.Bool("linkshared", false, "generate code that will be linked against Go shared libraries")
	AllErrors  = Set.Bool("e", false, "no limit on number of errors reported")
	SymABIs    = Set.Bool("gensymabis", false, "write symbol ABI information to output file, don't assemble")
	Importpath = Set.String("p", obj.UnlinkablePkg, "set expected package import to path")
	Spectre    = Set.String("spectre", "", "enable spectre mitigations in `list` (all, ret)")
	Std        = Set.Bool("std", false, "building standard library")
)

var DebugFlags struct {
	CompressInstructions int    `help:"use compressed instructions when possible (if supported by architecture)"`
	MayMoreStack         string `help:"call named function before all stack growth checks"`
	PCTab                string `help:"print named pc-value table\nOne of: pctospadj, pctofile, pctoline, pctoinline, pctopcdata"`
}

var (
	D        MultiFlag
	I        MultiFlag
	PrintOut int
	DebugV   bool
)

func init() {
	Set.Var(&D, "D", "predefined symbol with optional simple value -D=identifier=value; can be set multiple times")
	Set.Var(&I, "I", "include directory; can be set multiple times")
	Set.BoolVar(&DebugV, "v", false, "print debug output")
	Set.Var(objabi.NewDebugFlag(&DebugFlags, nil), "d", "enable debugging settings; try -d help")

	DebugFlags.CompressInstructions = 1
}

// MultiFlag allows setting a value multiple times to collect a list, as in -I=dir1 -I=dir2.
type MultiFlag []string

func (m *MultiFlag) String() string {
	if len(*m) == 0 {
		return ""
	}
	return fmt.Sprint(*m)
}

func (m *MultiFlag) Set(val string) error {
	(*m) = append(*m, val)
	return nil
}

func Usage() {
	fmt.Fprintf(os.Stderr, "usage: asm [options] file.s ...\n")
	fmt.Fprintf(os.Stderr, "Flags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func Parse() {
	objabi.AddVersionFlag() // -V
	objabi.Flagcount("S", "print assembly and machine code", &PrintOut)
	objabi.Flagparse(Usage)
	if flag.NArg() == 0 {
		flag.Usage()
	}

	// Flag refinement.
	if *OutputFile == "" {
		if flag.NArg() != 1 {
			flag.Usage()
		}
		input := filepath.Base(flag.Arg(0))
		input = strings.TrimSuffix(input, ".s")
		*OutputFile = fmt.Sprintf("%s.o", input)
	}
}
