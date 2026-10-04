// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import "testing"

func TestLLDVersion(t *testing.T) {
	for out, want := range map[string]string{
		"Ubuntu LLD 18.1.8 (compatible with GNU linkers)\n":   "18.1.8",
		"Homebrew LLD 18.1.8 (compatible with GNU linkers)\n": "18.1.8",
		"LLD 18.1.3\n": "18.1.3",
		"ld64.lld: unknown argument\n": "",
	} {
		if got := lldVersion(out); got != want {
			t.Errorf("lldVersion(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestParseZigLibDir(t *testing.T) {
	// The shape zig 0.16.0 prints, cut to the fields around lib_dir.
	env := ".{\n    .zig_exe = \"/opt/zig/zig\",\n    .lib_dir = \"/opt/zig/lib\",\n    .version = \"0.16.0\",\n}\n"
	dir, err := parseZigLibDir(env)
	if err != nil || dir != "/opt/zig/lib" {
		t.Errorf("parseZigLibDir = %q, %v; want /opt/zig/lib", dir, err)
	}

	// A Windows path comes with its backslashes escaped.
	dir, err = parseZigLibDir("    .lib_dir = \"C:\\\\zig\\\\lib\",\n")
	if err != nil || dir != `C:\zig\lib` {
		t.Errorf("parseZigLibDir = %q, %v; want C:\\zig\\lib", dir, err)
	}

	if dir, err := parseZigLibDir(".{\n    .version = \"0.16.0\",\n}\n"); err == nil {
		t.Errorf("parseZigLibDir with no lib_dir = %q, want an error", dir)
	}
}
