// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package work

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeArchive writes an archive holding the export data and object entries
// the compiler writes, each opening with a header that names buildID.
func writeArchive(t *testing.T, name, buildID, export, object string) string {
	t.Helper()
	var text strings.Builder
	text.WriteString("!<arch>\n")
	for _, entry := range []struct{ name, body string }{
		{"__.PKGDEF", fmt.Sprintf("go object\nbuild id %q\n\n%s", buildID, export)},
		{"_go_.o", fmt.Sprintf("go object\nbuild id %q\n\n%s", buildID, object)},
	} {
		fmt.Fprintf(&text, "%-16s%-12s%-6s%-6s%-8s%-10d`\n", entry.name, "0", "0", "0", "644", len(entry.body))
		text.WriteString(entry.body)
		if len(entry.body)%2 != 0 {
			text.WriteByte('\n')
		}
	}
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, []byte(text.String()), 0o666); err != nil {
		t.Fatal(err)
	}
	return file
}

func mustExportID(t *testing.T, file, buildID string) string {
	t.Helper()
	id, err := exportID(file, buildID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestExportIDReadsOnlyTheExportData(t *testing.T) {
	const first = "aaaaaaaaaaaaaaaaaaaa/aaaaaaaaaaaaaaaaaaaa"
	const second = "bbbbbbbbbbbbbbbbbbbb/bbbbbbbbbbbbbbbbbbbb"
	original := mustExportID(t, writeArchive(t, "original.a", first, "types", "code one"), first)

	if id := mustExportID(t, writeArchive(t, "body.a", first, "types", "code two"), first); id != original {
		t.Errorf("a changed function body moved the export ID: %s, want %s", id, original)
	}
	if id := mustExportID(t, writeArchive(t, "rebuilt.a", second, "types", "code two"), second); id != original {
		t.Errorf("a different build ID moved the export ID: %s, want %s", id, original)
	}
	if id := mustExportID(t, writeArchive(t, "export.a", first, "other types", "code one"), first); id == original {
		t.Errorf("changed export data kept the export ID %s", id)
	}
}

func TestExportIDRefusesAnArchiveWithoutExportData(t *testing.T) {
	file := filepath.Join(t.TempDir(), "object.a")
	text := fmt.Sprintf("!<arch>\n%-16s%-12s%-6s%-6s%-8s%-10d`\nxx", "_go_.o", "0", "0", "0", "644", 2)
	if err := os.WriteFile(file, []byte(text), 0o666); err != nil {
		t.Fatal(err)
	}
	if id, err := exportID(file, "id"); err == nil {
		t.Fatalf("exportID of an archive with no __.PKGDEF = %s, want an error", id)
	}
}
