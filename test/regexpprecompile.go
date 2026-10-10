// run

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that a pattern the compiler precompiles behaves like one that
// compiles at run time.

package main

import (
	"reflect"
	"regexp"
)

// variable holds patterns the compiler cannot resolve.
var variable = []string{`a|ab`, `(?P<first>x+)(y)`, `[[:alpha:]]+`, `a(`}

func pattern() string { return `(?P<first>x+)` }

func main() {
	// Each call returns its own Regexp, and Longest changes only its own.
	one := regexp.MustCompile(`a|ab`)
	two := regexp.MustCompile(`a|ab`)
	if one == two {
		panic("two calls returned the same Regexp")
	}
	one.Longest()
	if got := one.FindString("ab"); got != "ab" {
		panic("Longest had no effect: " + got)
	}
	if got := two.FindString("ab"); got != "a" {
		panic("Longest changed another Regexp: " + got)
	}
	run, _ := regexp.Compile(variable[0])
	if !reflect.DeepEqual(*two, *run) {
		panic("precompiled a|ab differs from the run-time one")
	}

	// SubexpNames returns a slice that belongs to the one Regexp.
	named := regexp.MustCompile(pattern() + `(y)`)
	names := named.SubexpNames()
	names[1] = "changed"
	if again := regexp.MustCompile(pattern() + `(y)`); again.SubexpNames()[1] != "first" {
		panic("SubexpNames is shared between calls")
	}
	run, _ = regexp.Compile(variable[1])
	if !reflect.DeepEqual(regexp.MustCompile(pattern()+`(y)`), run) {
		panic("precompiled named groups differ from the run-time ones")
	}

	// POSIX syntax and leftmost-longest matching.
	posix := regexp.MustCompilePOSIX(`a|ab`)
	if got := posix.FindString("ab"); got != "ab" {
		panic("MustCompilePOSIX is not leftmost-longest: " + got)
	}
	class, err := regexp.CompilePOSIX(`[[:alpha:]]+`)
	if err != nil || class.FindString("12abc3") != "abc" {
		panic("CompilePOSIX of a constant failed")
	}
	runPOSIX, _ := regexp.CompilePOSIX(variable[2])
	if !reflect.DeepEqual(*class, *runPOSIX) {
		panic("precompiled POSIX class differs from the run-time one")
	}

	// Compile of an invalid constant still returns the run-time error.
	bad, err := regexp.Compile(`a(`)
	_, runErr := regexp.Compile(variable[3])
	if bad != nil || err == nil || err.Error() != runErr.Error() {
		panic("Compile of an invalid constant does not return its error")
	}
}
