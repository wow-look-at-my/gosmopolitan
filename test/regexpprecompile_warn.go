// errorcheck -0

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that a MustCompile pattern the compiler cannot resolve gets a
// performance warning, and that a pattern it can resolve gets none.

package p

import (
	"os"
	"regexp"
)

func Dynamic(pat string) *regexp.Regexp {
	return regexp.MustCompile(pat) // ERROR "performance warning: the pattern of regexp.MustCompile is not constant, so it compiles at run time"
}

func DynamicPOSIX() *regexp.Regexp {
	return regexp.MustCompilePOSIX(os.Args[0]) // ERROR "performance warning: the pattern of regexp.MustCompilePOSIX is not constant"
}

var global = `^x`

func Global() *regexp.Regexp {
	return regexp.MustCompile(global) // ERROR "performance warning"
}

const prefix = `^a`

func Constant() *regexp.Regexp {
	local := prefix + `+`
	return regexp.MustCompile(local + `$`)
}

func Value() *regexp.Regexp {
	compile := regexp.MustCompile
	return compile(`b+`)
}

// Compile exists for patterns that arrive at run time, so it gets no warning.
func Runtime(pat string) (*regexp.Regexp, error) {
	return regexp.Compile(pat)
}
