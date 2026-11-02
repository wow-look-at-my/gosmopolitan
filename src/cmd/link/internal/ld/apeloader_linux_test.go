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
	"strconv"
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
	hash := strings.Repeat("ab", 32)
	l := &apeLoader{name: "apeld-darwin-arm64", blob: apeGzip(stand, "apeld-darwin-arm64"), gzip: true, tag: "abababab", hash: hash, named: true}
	prog := filepath.Join(dir, "myprog.com")
	if err := os.WriteFile(prog, l.blob, 0o755); err != nil {
		t.Fatal(err)
	}
	// The cache lives only under a sticky directory, where no other user can
	// rename an entry between the check and the exec.
	cache := filepath.Join(dir, "cache")
	if err := os.Mkdir(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cache, 0o755|os.ModeSticky); err != nil {
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

	// The first launch runs the unpacked copy from the unpack directory, under
	// the program's name, with -i and the cache directory. The real loader
	// then checks its hash, makes the cache and moves itself in. The
	// stand-in does not. No mkdir runs on this path.
	entry := filepath.Join(cache, ".ape-apeld-darwin-arm64-"+hash+"-"+strconv.Itoa(os.Getuid()))
	got := boot()
	if want := []string{filepath.Join(cache, "myprog.com"), "-i", entry, prog, "one arg", "two"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("the first launch ran %q, want %q", got, want)
	}
	if _, err := os.Stat(entry); err == nil {
		t.Errorf("the first launch made %s, which the loader makes", entry)
	}

	// When the program's name is taken in the unpack directory, the copy goes
	// to a per-run directory of the cache, and the name stays untouched.
	taken := got[0]
	if err := os.WriteFile(taken, []byte("taken"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = boot()
	if want := []string{"-i", entry, prog, "one arg", "two"}; len(got) != 6 || strings.Join(got[1:], "|") != strings.Join(want, "|") {
		t.Fatalf("the launch with the name taken ran %q, want its path, then %q", got, want)
	}
	if filepath.Base(got[0]) != "myprog.com" || filepath.Dir(filepath.Dir(got[0])) != entry {
		t.Errorf("the launch with the name taken ran %q, want <cache>/.<pid>/myprog.com under %s", got[0], entry)
	}
	if fi, err := os.Stat(entry); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the cache directory is %v (%v), want mode 0700", fi, err)
	}
	if data, err := os.ReadFile(taken); err != nil || string(data) != "taken" {
		t.Errorf("the launch changed %s: %q, %v", taken, data, err)
	}

	// Once the loader is in place, a launch reads nothing out of the APE.
	if err := os.Rename(got[0], filepath.Join(entry, "myprog.com")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prog, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	again := boot()
	if want := []string{filepath.Join(entry, "myprog.com"), prog, "one arg", "two"}; strings.Join(again, "|") != strings.Join(want, "|") {
		t.Errorf("the cached launch ran %q, want %q", again, want)
	}

	// A cache under a directory that is not sticky is never used.
	if err := os.Chmod(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", script.String(), prog)
	cmd.Env = append(os.Environ(), "APE_LOADER=", "APE_LOADERDIR="+cache, "PATH=/usr/bin:/bin")
	if out, err := cmd.CombinedOutput(); err == nil || strings.Contains(string(out), "myprog.com|") {
		t.Errorf("a cache under a directory that is not sticky ran: %v\n%s", err, out)
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
