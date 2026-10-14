// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build linux && (amd64 || arm64)

package ld

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Root registers the loader for the APE magic system-wide. A foreign APE, such
// as a cosmocc tool, then reaches it with no payload this loader can boot. It
// must run that file as the kernel would with no entry: under /bin/sh, with
// its arguments unchanged.
func TestApeLoaderRunsAForeignAPEUnderTheShell(t *testing.T) {
	name := "apeld-linux-" + runtime.GOARCH
	dir := t.TempDir()
	loader := filepath.Join(dir, name)
	if err := os.WriteFile(loader, apeLoaderBins()[name], 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "foreign.com")
	script := "MZqFpD='\n'\nprintf 'foreign:%s|%s\\n' \"$1\" \"$2\"\n"
	if err := os.WriteFile(foreign, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(loader, foreign, "one arg", "two").CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, foreign, err, out)
	}
	if got, want := string(out), "foreign:one arg|two\n"; got != want {
		t.Fatalf("%s ran the foreign APE and it printed %q, want %q", name, got, want)
	}
}
