// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"internal/testenv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cmd/internal/buildid"
)

// TestParseToolID exercises the parsing of tool "-V=full" output into tool IDs.
func TestParseToolID(t *testing.T) {
	tests := []struct {
		name      string
		isVetTool bool
		line      string
		want      string
		ok        bool
	}{
		// Plain releases use the whole line.
		{"compile", false, "compile version go1.9.1\n", "compile version go1.9.1", true},
		{"compile", false, "compile version go1.9.1 X:framepointer\n", "compile version go1.9.1 X:framepointer", true},
		// Development branches use the content ID part of the tool's build ID.
		{"compile", false, "compile version devel go1.99-abc buildID=aaaa/bbbb/cccc/dddd\n", "dddd", true},
		// The cosmo fork stamps the same release-style version into every
		// build, so its tools report their own build ID too, and the tool ID
		// is the content ID part — never the constant version line.
		{"compile", false, "compile version go1.26.4cosmo buildID=aaaa/bbbb/cccc/dddd\n", "dddd", true},
		{"compile", false, "compile version go1.26.4cosmo X:fieldtrack buildID=aaaa/bbbb/cccc/dddd\n", "dddd", true},
		{"link", false, "link version go1.26.4cosmo buildID=ee/ff\n", "ff", true},
		// An alternative vet tool may print any leading name.
		{"vet", true, "myanalyzer version devel comments-go-here buildID=11/22\n", "22", true},
		// Malformed lines.
		{"compile", false, "", "", false},
		{"compile", false, "compile version\n", "", false},
		{"asm", false, "compile version go1.9.1\n", "", false},
		{"compile", false, "compile version devel go1.99-abc\n", "", false}, // devel requires buildID=
	}
	for _, tt := range tests {
		id, ok := parseToolID(tt.name, tt.isVetTool, tt.line)
		if id != tt.want || ok != tt.ok {
			t.Errorf("parseToolID(%q, %v, %q) = %q, %v; want %q, %v",
				tt.name, tt.isVetTool, tt.line, id, ok, tt.want, tt.ok)
		}
	}

	// The incident property, at the parser level: two fork builds report the
	// same version but different build IDs, and must get different tool IDs.
	id1, _ := parseToolID("compile", false, "compile version go1.26.4cosmo buildID=aa/bb\n")
	id2, _ := parseToolID("compile", false, "compile version go1.26.4cosmo buildID=aa/cc\n")
	if id1 == id2 {
		t.Errorf("tool IDs for same-version, different-buildID tools collide: %q", id1)
	}
}

// TestCosmoToolIDTracksToolContent is the regression test for the 2026-07-20
// consumer cache-poisoning incident. The fork stamps the same release-style
// version (go1.26.4cosmo) into every build, so cmd/go's tool IDs used to be
// identical for any two fork builds. Action IDs therefore collided across
// builds, and build caches — a consumer's local GOCACHE, or a shared cache
// tier that survives across toolchain updates — served objects
// compiled by an older fork build into links done by a newer one, producing
// binaries that crash at startup.
//
// The test takes the real compile binary and makes a second copy whose
// content differs (its embedded build ID rewritten — a stand-in for a
// genuinely different fork build, which differs in exactly this way and
// more). Both claim the same version, but cmd/go must compute different tool
// IDs for them. Before the fix, -V=full printed only the constant version
// line for both, so the tool IDs were equal and this test fails.
func TestCosmoToolIDTracksToolContent(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	testenv.MustHaveExec(t)
	if !strings.Contains(runtime.Version(), "cosmo") {
		t.Skipf("test exercises the cosmo fork's tool ID scheme; running under %s", runtime.Version())
	}

	src := filepath.Join(testenv.GOROOT(t), "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, "compile")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	// Two copies in separate directories so both run as plain "compile"
	// (tools print their argv[0] basename in the -V=full line).
	dir := t.TempDir()
	tool1 := filepath.Join(dir, "build1", "compile")
	tool2 := filepath.Join(dir, "build2", "compile")
	for _, name := range []string{tool1, tool2} {
		if err := os.MkdirAll(filepath.Dir(name), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Give tool2 different content by rewriting its embedded build ID, the
	// same way cmd/go's updateBuildID writes final build IDs. Every non-'/'
	// byte of the ID changes, so the content ID half necessarily differs.
	oldID, err := buildid.ReadFile(tool2)
	if err != nil {
		t.Fatal(err)
	}
	newID := strings.Map(func(r rune) rune {
		switch r {
		case '/':
			return '/'
		case 'x':
			return 'y'
		default:
			return 'x'
		}
	}, oldID)
	if newID == oldID || len(newID) != len(oldID) {
		t.Fatalf("failed to construct a distinct same-length build ID from %q", oldID)
	}
	f, err := os.OpenFile(tool2, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	matches, _, err := buildid.FindAndHash(f, oldID, 0)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	if len(matches) == 0 {
		f.Close()
		t.Fatalf("no occurrences of build ID %q found in %s", oldID, tool2)
	}
	if err := buildid.Rewrite(f, matches, newID); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	runV := func(tool string) string {
		out, err := testenv.Command(t, tool, "-V=full").Output()
		if err != nil {
			t.Fatalf("%s -V=full: %v", tool, err)
		}
		return string(out)
	}
	out1 := runV(tool1)
	out2 := runV(tool2)

	// Both tools claim the same name and version...
	f1, f2 := strings.Fields(out1), strings.Fields(out2)
	if len(f1) < 3 || len(f2) < 3 || f1[0] != f2[0] || f1[1] != f2[1] || f1[2] != f2[2] {
		t.Fatalf("tools report different name/version:\n\t%q\n\t%q", out1, out2)
	}

	// ...but cmd/go must compute different tool IDs for them.
	id1, ok := parseToolID("compile", false, out1)
	if !ok {
		t.Fatalf("parseToolID failed for %q", out1)
	}
	id2, ok := parseToolID("compile", false, out2)
	if !ok {
		t.Fatalf("parseToolID failed for %q", out2)
	}
	if id1 == id2 {
		t.Errorf("two content-different compile binaries with the same version produced the same tool ID %q:\n\t%q\n\t%q\n"+
			"different fork builds would collide in the build cache and serve each other stale objects",
			id1, out1, out2)
	}
}
