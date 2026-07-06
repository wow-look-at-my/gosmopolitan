// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

type envFlags []string

func (e *envFlags) String() string { return strings.Join(*e, ",") }
func (e *envFlags) Set(v string) error {
	if !strings.Contains(v, "=") {
		return fmt.Errorf("want KEY=VALUE, got %q", v)
	}
	*e = append(*e, v)
	return nil
}

func main() {
	var env envFlags
	trace := flag.Bool("trace", false, "log host-side socket activity to stderr")
	inherit := flag.Bool("env-inherit", false, "pass the host environment to the module")
	flag.Var(&env, "env", "environment variable KEY=VALUE for the module (repeatable)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] module.wasm [args...]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "runs a GOWASI=wasmedgesock wasip1 module with real TCP networking\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	wasm, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	moduleEnv := []string(env)
	if *inherit {
		moduleEnv = append(os.Environ(), moduleEnv...)
	}
	code, err := Run(context.Background(), RunConfig{
		Module: wasm,
		Args:   flag.Args(),
		Env:    moduleEnv,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Trace:  *trace,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}
