// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"

	"./a"
)

func main() {
	bad := 0
	check := func(got, want string) {
		if got != want {
			fmt.Printf("got %q, want %q\n", got, want)
			bad++
		}
	}
	check(a.Repeat(), "xx!")
	check(a.Repeat("y"), "yy!")
	check(a.Repeat("y", 1), "y!")
	check(a.Repeat("y", 1, false), "y")
	check(fmt.Sprint(a.T{}.Scale()), "9")
	check(fmt.Sprint(a.T{}.Scale(4)), "16")
	if bad != 0 {
		os.Exit(1)
	}
}
