// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || (js && wasm)

package mime

import (
	"testing"
)

func initMimeUnixTest(t *testing.T) {
	once.Do(initMime)
	// The globs2 loader keeps the FIRST type it sees for an extension,
	// so a host whose own database names one of these extensions wins
	// over the testdata below and the test measures that host. macOS
	// ships /etc/apache2/mime.types, which names .t3. Resetting to the
	// builtin table leaves the testdata as the only other source.
	setMimeTypes(builtinTypesLower, builtinTypesLower)
	err := loadMimeGlobsFile("testdata/test.types.globs2")
	if err != nil {
		t.Fatal(err)
	}

	loadMimeFile("testdata/test.types")
}

func TestTypeByExtensionUNIX(t *testing.T) {
	t.Serial() // This test replaces the package's mime table and its sync.Once.
	initMimeUnixTest(t)
	typeTests := map[string]string{
		".T1":       "application/test",
		".t2":       "text/test; charset=utf-8",
		".t3":       "document/test",
		".t4":       "example/test",
		".png":      "image/png",
		",v":        "",
		"~":         "",
		".foo?ar":   "",
		".foo*r":    "",
		".foo[1-3]": "",
	}

	for ext, want := range typeTests {
		val := TypeByExtension(ext)
		if val != want {
			t.Errorf("TypeByExtension(%q) = %q, want %q", ext, val, want)
		}
	}
}
