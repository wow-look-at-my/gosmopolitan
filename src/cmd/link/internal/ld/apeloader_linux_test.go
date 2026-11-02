// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build linux && (amd64 || arm64)

package ld

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// XNU names a process after the file it execs, and top, Activity Monitor
// and btop show that name. So the darwin boot runs the loader from a cached
// copy with the program's file name. The darwin loader cannot run here. A
// stand-in, packed the way the APE packs the loader, shows the path and
// the argv the boot execs it with.
func TestApeDarwinBootNamesTheLoaderAfterTheProgram(t *testing.T) {
	dir := t.TempDir()
	// A loader with no arguments exits 127, which the boot takes as proof it runs.
	stand := []byte("#!/bin/sh\n[ $# -eq 0 ] && exit 127\nprintf '%s|' \"$0\" \"$@\"\n")
	l := &apeLoader{name: "apeld-darwin-arm64", blob: apeGzip(stand, "apeld-darwin-arm64"), gzip: true, tag: "0123abcd", named: true}
	prog := filepath.Join(dir, "myprog.com")
	if err := os.WriteFile(prog, l.blob, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache")
	if err := os.Mkdir(cache, 0o755); err != nil {
		t.Fatal(err)
	}

	var script bytes.Buffer
	script.WriteString("apepath() { :; }\no=$0\n")
	writeLoaderBoot(&script, l)
	boot := func() []string {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-c", script.String(), prog, "one arg", "two")
		cmd.Env = append(os.Environ(), "APE_LOADER=", "APE_LOADERDIR="+cache)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the boot failed: %v\n%s", err, out)
		}
		return strings.Split(strings.TrimSuffix(string(out), "|"), "|")
	}

	got := boot()
	if want := []string{prog, "one arg", "two"}; len(got) != 4 || strings.Join(got[1:], "|") != strings.Join(want, "|") {
		t.Fatalf("the loader ran with %q, want its path, then %q", got, want)
	}
	if filepath.Base(got[0]) != "myprog.com" {
		t.Errorf("the loader ran from %q, want a file named after the program", got[0])
	}
	held, err := filepath.Glob(filepath.Join(cache, ".ape-apeld-darwin-arm64-0123abcd-*", "*"))
	if err != nil || len(held) != 1 || held[0] != got[0] {
		t.Errorf("the cache holds %q, want only %s", held, got[0])
	}
	if fi, err := os.Stat(filepath.Dir(got[0])); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the cache directory is %v (%v), want mode 0700", fi.Mode(), err)
	}

	// A launch after the first reads nothing out of the APE.
	if err := os.WriteFile(prog, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if again := boot(); strings.Join(again, "|") != strings.Join(got, "|") {
		t.Errorf("the second launch ran %q, want the cached %q", again, got)
	}
}

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
