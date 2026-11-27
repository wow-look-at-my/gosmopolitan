// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package ld

import "testing"

func TestExpandSourceDir(t *testing.T) {
	t.Serial("the module directory table is a package global that every file table of a link in this process reads")
	saved := sourceDirs
	defer func() { sourceDirs = saved }()
	sourceDirs = nil
	addSourceDir("example.com/m", "/home/user/checkout")
	addSourceDir("example.com/m/other@v0.0.0", "/home/user/elsewhere")

	tests := []struct {
		about string
		file  string
		want  string
	}{
		{"a file of the root package", "example.com/m/m.go", "/home/user/checkout/m.go"},
		{"a file of a package below", "example.com/m/internal/alpha/alpha.go", "/home/user/checkout/internal/alpha/alpha.go"},
		{"a module a replace points at a directory", "example.com/m/other@v0.0.0/part/part.go", "/home/user/elsewhere/part/part.go"},
		{"a module cache module under the path of the main one", "example.com/m/v2@v2.0.0/m.go", "example.com/m/v2@v2.0.0/m.go"},
		{"a module with the same prefix and another name", "example.com/more/m.go", "example.com/more/m.go"},
		{"a file of the standard library", "/usr/local/go/src/fmt/print.go", "/usr/local/go/src/fmt/print.go"},
	}
	for _, test := range tests {
		if got := expandSourceDir(test.file); got != test.want {
			t.Errorf("%s: expandSourceDir(%q) = %q, want %q", test.about, test.file, got, test.want)
		}
	}

	// A link with -trimpath is given no directory, and every name stays.
	sourceDirs = nil
	if got := expandSourceDir("example.com/m/m.go"); got != "example.com/m/m.go" {
		t.Errorf("expandSourceDir with no directory = %q, want the name unchanged", got)
	}
}
