// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modfetch

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cmd/go/internal/cache"
	"cmd/go/internal/gendep"

	"golang.org/x/mod/module"
)

// validSum answers a checksum shaped as the cache's h1 sums are. decodeOverlay
// holds a header to that shape, so a test header carries a real one.
func validSum(fill byte) string {
	return "h1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, sha256.Size))
}

// overlayFile is one file of a zip built in memory.
type overlayFile struct {
	name, body string
}

// makeZip archives files in the given order. The order decides which file
// zipDifference names first, so a test states it.
func makeZip(test *testing.T, files ...overlayFile) []byte {
	test.Helper()
	var buf bytes.Buffer
	out := zip.NewWriter(&buf)
	for _, file := range files {
		into, err := out.Create(file.name)
		if err != nil {
			test.Fatalf("creating %s: %v", file.name, err)
		}
		if _, err := into.Write([]byte(file.body)); err != nil {
			test.Fatalf("writing %s: %v", file.name, err)
		}
	}
	if err := out.Close(); err != nil {
		test.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

// An entry that goes into the cache comes back out of it saying the same thing.
func TestOverlayEntryRoundTrip(test *testing.T) {
	want := &overlayEntry{sum: validSum('a'), zip: makeZip(test, overlayFile{"m@v1.0.0/gen.go", "package m\n"})}
	got, err := decodeOverlay(want.encode())
	if err != nil {
		test.Fatalf("decodeOverlay: %v", err)
	}
	if got.sum != want.sum {
		test.Errorf("sum = %q, want %q", got.sum, want.sum)
	}
	if !bytes.Equal(got.zip, want.zip) {
		test.Errorf("zip is %d bytes, want the %d written", len(got.zip), len(want.zip))
	}
}

// An empty overlay is meaningful: it says the module needs nothing, and it is
// what lets the next build skip generation. So it survives the cache too.
func TestOverlayEntryRoundTripEmpty(test *testing.T) {
	want := &overlayEntry{sum: validSum('b'), zip: makeZip(test)}
	got, err := decodeOverlay(want.encode())
	if err != nil {
		test.Fatalf("decodeOverlay: %v", err)
	}
	if got.sum != want.sum || !bytes.Equal(got.zip, want.zip) {
		test.Errorf("got sum %q and %d bytes, want %q and %d", got.sum, len(got.zip), want.sum, len(want.zip))
	}
}

// The cache is shared, so a body that is not an entry of this version is
// refused rather than read as one.
func TestDecodeOverlayRejectsMalformedHeaders(test *testing.T) {
	cases := []struct {
		why  string
		body string
	}{
		{"no header line at all", overlayVersion + " " + validSum('c')},
		{"another version of the entry", "overlay v0 " + validSum('c') + "\n"},
		{"the checksum is missing", overlayVersion + "\n"},
		{"a field follows the checksum", overlayVersion + " " + validSum('c') + " extra\n"},
		{"the checksum is not an h1 sum", overlayVersion + " h1:short\n"},
		{"the header names another format", "modzip v1 " + validSum('c') + "\n"},
	}
	for _, tcase := range cases {
		if _, err := decodeOverlay([]byte(tcase.body)); err == nil {
			test.Errorf("decodeOverlay accepted an entry where %s", tcase.why)
		}
	}
}

// A build that stops over an overlay the cache disagrees with names the file
// that differs, so whoever reads the message knows what to look at.
func TestZipDifference(test *testing.T) {
	cases := []struct {
		why         string
		left, right []byte
		want        string
	}{
		{
			why:   "only the cache's copy carries the file",
			left:  makeZip(test, overlayFile{"m@v1/a.go", "1"}, overlayFile{"m@v1/b.go", "2"}),
			right: makeZip(test, overlayFile{"m@v1/a.go", "1"}),
			want:  "the cache's copy carries m@v1/b.go and this one does not",
		},
		{
			why:   "only this copy carries the file",
			left:  makeZip(test, overlayFile{"m@v1/a.go", "1"}),
			right: makeZip(test, overlayFile{"m@v1/a.go", "1"}, overlayFile{"m@v1/b.go", "2"}),
			want:  "this copy carries m@v1/b.go and the cache's does not",
		},
		{
			why:   "both carry the file and its bytes differ",
			left:  makeZip(test, overlayFile{"m@v1/a.go", "one"}),
			right: makeZip(test, overlayFile{"m@v1/a.go", "two"}),
			want:  "m@v1/a.go differs",
		},
	}
	for _, tcase := range cases {
		if got := zipDifference(tcase.left, tcase.right); got != tcase.want {
			test.Errorf("zipDifference where %s = %q, want %q", tcase.why, got, tcase.want)
		}
	}
}

// The overlay carries the added files from the machine that generated them to
// every machine that reads the cache, so what it packs is what arrives.
func TestOverlayRoundTripsAddedFiles(test *testing.T) {
	mod := module.Version{Path: "example.com/m", Version: "v1.2.3"}
	added := []string{"gen.go", "internal/deep/table.go"}
	bodies := map[string]string{
		"gen.go":                 "package m\n\nconst Generated = 1\n",
		"internal/deep/table.go": "package deep\n\nvar Table = []int{1, 2, 3}\n",
	}

	from := test.TempDir()
	for _, rel := range added {
		path := filepath.Join(from, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(bodies[rel]), 0o666); err != nil {
			test.Fatal(err)
		}
	}

	overlay, err := packOverlay(mod, from, added)
	if err != nil {
		test.Fatalf("packOverlay: %v", err)
	}
	into := test.TempDir()
	if err := applyOverlay(mod, into, overlay); err != nil {
		test.Fatalf("applyOverlay: %v", err)
	}
	for _, rel := range added {
		got, err := os.ReadFile(filepath.Join(into, filepath.FromSlash(rel)))
		if err != nil {
			test.Errorf("reading %s: %v", rel, err)
			continue
		}
		if string(got) != bodies[rel] {
			test.Errorf("%s = %q, want %q", rel, got, bodies[rel])
		}
	}
}

// The entry decides what it writes and the cache is shared, so a name outside
// the module's own directory is refused rather than written.
func TestApplyOverlayRefusesNamesOutsideTheModule(test *testing.T) {
	mod := module.Version{Path: "example.com/m", Version: "v1.2.3"}
	prefix := mod.Path + "@" + mod.Version + "/"
	cases := []struct {
		why        string
		name, body string
	}{
		{"the name is under another module", "other.com/n@v1.0.0/gen.go", "package evil\n"},
		{"the name is the prefix and nothing else", prefix, ""},
		{"the name walks out of the module", prefix + "../../escaped.go", "package evil\n"},
	}
	for _, tcase := range cases {
		into := test.TempDir()
		err := applyOverlay(mod, into, makeZip(test, overlayFile{tcase.name, tcase.body}))
		if err == nil {
			test.Errorf("applyOverlay accepted %q, where %s", tcase.name, tcase.why)
			continue
		}
		if !strings.Contains(err.Error(), "outside the module") {
			test.Errorf("applyOverlay(%q) said %v, want it to say the name is outside the module", tcase.name, err)
		}
	}
}

// The key names the module and the base zip the overlay completes. Two modules,
// two versions, or two base zips are three different things to complete, and a
// key they shared would give the fleet one of them under the other's name.
func TestOverlayKeyNamesWhatItCompletes(test *testing.T) {
	mod := module.Version{Path: "example.com/m", Version: "v1.2.3"}
	base := validSum('a')
	want := overlayKey(mod, base)

	if got := overlayKey(mod, base); got != want {
		test.Errorf("the same module and base zip gave two keys")
	}
	others := []struct {
		why  string
		mod  module.Version
		base string
	}{
		{"another module path", module.Version{Path: "example.com/other", Version: mod.Version}, base},
		{"another version", module.Version{Path: mod.Path, Version: "v1.2.4"}, base},
		{"another base zip", mod, validSum('b')},
		{"no base zip at all", mod, ""},
	}
	for _, other := range others {
		if got := overlayKey(other.mod, other.base); got == want {
			test.Errorf("%s gave the same key", other.why)
		}
	}
	keys := []string{}
	for _, other := range others {
		key := overlayKey(other.mod, other.base)
		keys = append(keys, string(key[:]))
	}
	slices.Sort(keys)
	if len(slices.Compact(keys)) != len(others) {
		test.Errorf("two of the %d differing inputs share a key", len(others))
	}
}

// The version names how a module is completed, so every entry an earlier rule
// stored is out of reach: its key is another key, and its body is refused if
// anything hands it over anyway.
func TestOverlayVersionRetiresTheEntriesBeforeIt(test *testing.T) {
	mod := module.Version{Path: "example.com/m", Version: "v1.2.3"}
	base := validSum('a')
	for _, was := range []string{"overlay v3", "overlay v4"} {
		if was == overlayVersion {
			test.Fatalf("%q is the version in force, so it retires nothing", was)
		}
		hashed := cache.NewHash("modzip")
		fmt.Fprintf(hashed, "%s\n%s\n%s\n%s\n", was, mod.Path, mod.Version, base)
		if hashed.Sum() == overlayKey(mod, base) {
			test.Errorf("an entry stored under %q is read back under the key in force", was)
		}
		if _, err := decodeOverlay([]byte(was + " " + base + "\n")); err == nil {
			test.Errorf("an entry bodied as %q was decoded as one of this version", was)
		}
	}
}

func TestCompleteDirLeavesASupersededModuleAlone(test *testing.T) {
	// Superseded is package level, and this is the only test that writes it.
	test.Serial()

	dir := test.TempDir()
	pkg := filepath.Join(dir, "imports")
	if err := os.MkdirAll(pkg, 0o777); err != nil {
		test.Fatal(err)
	}
	// The directive names a program no host has, so completing this module
	// reports a failure. Skipping it is what this test reads.
	source := "//go:generate a-program-no-host-has\n\npackage imports\n"
	if err := os.WriteFile(filepath.Join(pkg, "imports.go"), []byte(source), 0o666); err != nil {
		test.Fatal(err)
	}
	if len(gendep.Packages(dir)) == 0 {
		test.Fatal("the directory carries no directive, so this would pass without the skip")
	}

	mod := module.Version{Path: "golang.org/x/tools", Version: "v0.11.0"}
	var asked []module.Version
	Superseded = func(other module.Version) bool {
		asked = append(asked, other)
		return true
	}
	test.Cleanup(func() { Superseded = nil })

	if err := (&Fetcher{}).completeDir(context.Background(), mod, dir); err != nil {
		test.Fatalf("completing a superseded module: %v", err)
	}
	if !slices.Equal(asked, []module.Version{mod}) {
		test.Errorf("asked about %v, want the one module being fetched", asked)
	}
}
