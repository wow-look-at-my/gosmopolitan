// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regexp

// The compiler replaces a call to Compile, CompilePOSIX, MustCompile or
// MustCompilePOSIX that has a constant pattern with a call to one of the
// functions below. The compiler builds tmpl with this package's own compile
// function and writes it out as read-only data. The second argument is the
// argument of the replaced call. The call keeps it, so its evaluation stays
// where the source put it. See cmd/compile/internal/regexpprecompile.

// precompiled returns a new Regexp copied from tmpl.
func precompiled(tmpl *Regexp, _ string) *Regexp {
	copied := *tmpl
	// SubexpNames returns this slice to the caller, and tmpl is read-only.
	copied.subexpNames = make([]string, len(tmpl.subexpNames))
	copy(copied.subexpNames, tmpl.subexpNames)
	return &copied
}

// precompiledErr is precompiled for a call that also returns an error.
func precompiledErr(tmpl *Regexp, expr string) (*Regexp, error) {
	return precompiled(tmpl, expr), nil
}
