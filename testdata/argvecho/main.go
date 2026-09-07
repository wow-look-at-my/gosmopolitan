// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// argvecho starts itself with three arguments and reports what the child
// received. A child that answers with fewer arguments than it was given has
// lost them between the two processes, which on an NT host takes down every
// test that forks: the child ignores -test.run and runs the whole package,
// and each test in it forks again.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const marker = "ARGVECHO_CHILD"

var want = []string{"-one=1", "two", "three"}

func main() {
	if os.Getenv(marker) != "" {
		fmt.Println(strings.Join(os.Args[1:], " "))
		return
	}

	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe, want...)
	cmd.Env = append(os.Environ(), marker+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("child failed: %v\n%s", err, out)
		os.Exit(1)
	}
	got := strings.TrimRight(string(out), "\r\n")
	if got != strings.Join(want, " ") {
		fmt.Printf("child received %q, want %q\n", got, strings.Join(want, " "))
		os.Exit(1)
	}
	fmt.Println("argv reached the child")
}
