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
// and btop show that name. So the darwin search runs an installed loader
// from a copy with the program's file name, in a directory of its own, with
// -u. The darwin loader cannot run here. A stand-in loader shows the path
// and the argv the search execs it with.
func TestApeDarwinSearchNamesTheLoaderAfterTheProgram(t *testing.T) {
	dir := t.TempDir()
	stand := filepath.Join(dir, "apeld-darwin-arm64")
	// A loader with no arguments exits 127, which the search takes as proof it runs.
	if err := os.WriteFile(stand, []byte("#!/bin/sh\n[ $# -eq 0 ] && exit 127\nprintf '%s|' \"$0\" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	unpack := filepath.Join(dir, "unpack")
	if err := os.Mkdir(unpack, 0o755); err != nil {
		t.Fatal(err)
	}
	prog := filepath.Join(dir, "myprog.com")

	var script bytes.Buffer
	script.WriteString("apereg() { return 1; }\napepath() { :; }\n")
	writeLoaderSearch(&script, apeBootData{Name: "apeld-darwin-arm64", Dirs: apeLoaderDirs, Tag: "0123abcd", Named: true})
	cmd := exec.Command("/bin/sh", "-c", "o=$0\n"+script.String(), prog, "one arg", "two")
	cmd.Env = append(os.Environ(), "APE_LOADER="+stand, "APE_LOADERDIR="+unpack)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the search failed: %v\n%s", err, out)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "|"), "|")
	if len(got) != 5 {
		t.Fatalf("the loader ran with %q, want its path, -u, the program and two arguments", got)
	}
	if filepath.Base(got[0]) != "myprog.com" {
		t.Errorf("the loader ran from %q, want a file named after the program", got[0])
	}
	if want := unpack + "/.ape-apeld-darwin-arm64-0123abcd."; !strings.HasPrefix(filepath.Dir(got[0])+".", want) {
		t.Errorf("the loader ran from %q, want a directory of its own under %s", got[0], want)
	}
	if want := []string{"-u", prog, "one arg", "two"}; strings.Join(got[1:], "|") != strings.Join(want, "|") {
		t.Errorf("the loader ran with arguments %q, want %q", got[1:], want)
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
