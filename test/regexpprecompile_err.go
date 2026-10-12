// errorcheck

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that an invalid constant pattern of MustCompile is a compile error.

package p

import "regexp"

var bad = regexp.MustCompile(`a(`) // ERROR "regexp.MustCompile\(`a\(`\) always panics: error parsing regexp: missing closing \): `a\(`"

func BadPOSIX() *regexp.Regexp {
	return regexp.MustCompilePOSIX(`(?i)x`) // ERROR "regexp.MustCompilePOSIX\(`\(\?i\)x`\) always panics: error parsing regexp: missing argument to repetition operator"
}

func pattern() string { return `x**` }

func Inlined() *regexp.Regexp {
	return regexp.MustCompile(pattern()) // ERROR "always panics: error parsing regexp: invalid nested repetition operator"
}

// Compile returns the error, so an invalid constant is legal there.
func Compile() error {
	_, err := regexp.Compile(`a(`)
	return err
}
