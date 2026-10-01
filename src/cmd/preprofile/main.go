// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Preprofile creates an intermediate representation of a pprof profile for use
// during PGO in the compiler. This transformation depends only on the profile
// itself and is thus wasteful to perform in every invocation of the compiler.
//
// Usage:
//
//	go tool preprofile [-V] [-o output] -i input
package preprofile

import (
	"bufio"
	"cmd/internal/objabi"
	"cmd/internal/pgo"
	"cmd/internal/telemetry/counter"
	"flag"
	"fmt"
	"log"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: go tool preprofile [-V] [-o output] -i input\n\n")
	flag.PrintDefaults()
	os.Exit(2)
}

// flagSet is preprofile's command line, one set of its own so preprofile can
// be linked beside the other tools; Main installs it before parsing.
var flagSet = flag.NewFlagSet("preprofile", flag.ExitOnError)

var (
	output = flagSet.String("o", "", "output file path")
	input  = flagSet.String("i", "", "input pprof file path")
)

func preprocess(profileFile string, outputFile string) error {
	f, err := os.Open(profileFile)
	if err != nil {
		return fmt.Errorf("error opening profile: %w", err)
	}
	defer f.Close()

	r := bufio.NewReader(f)
	d, err := pgo.FromPProf(r)
	if err != nil {
		return fmt.Errorf("error parsing profile: %w", err)
	}

	var out *os.File
	if outputFile == "" {
		out = os.Stdout
	} else {
		out, err = os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("error creating output file: %w", err)
		}
		defer out.Close()
	}

	w := bufio.NewWriter(out)
	if _, err := d.WriteTo(w); err != nil {
		return fmt.Errorf("error writing output file: %w", err)
	}

	return nil
}

// Main runs preprofile with args, the command line after the program name,
// and answers its exit status. A failure exits the process from inside
// preprofile, as it always has.
func Main(args []string) int {
	objabi.Enter("preprofile", args, flagSet)
	objabi.AddVersionFlag()

	log.SetFlags(0)
	log.SetPrefix("preprofile: ")
	counter.Open()

	flag.Usage = usage
	flag.Parse()
	counter.Inc("preprofile/invocations")
	counter.CountFlags("preprofile/flag:", *flag.CommandLine)
	if *input == "" {
		log.Print("Input pprof path required (-i)")
		usage()
	}

	if err := preprocess(*input, *output); err != nil {
		log.Fatal(err)
	}
	return 0
}
