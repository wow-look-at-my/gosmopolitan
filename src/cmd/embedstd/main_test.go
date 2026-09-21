// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package embedstd

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"internal/cosmo/embedded"
)

// TestIncludeHeadersFromTree reads the headers of a source tree's
// pkg/include.
func TestIncludeHeadersFromTree(t *testing.T) {
	goroot := t.TempDir()
	include := filepath.Join(goroot, "pkg", "include")
	if err := os.MkdirAll(include, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(include, "textflag.h"), []byte("#define NOSPLIT 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(include, "funcdata.h"), []byte("#define FUNCDATA_ArgsPointerMaps 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertHeaders(t, includeHeaders(goroot))
}

// TestIncludeHeadersFromBinary reads the headers of a GOROOT that names a
// go command carrying its standard library, which is what every pass of a
// self-hosted build after the first one builds with.
func TestIncludeHeadersFromBinary(t *testing.T) {
	var writer embedded.Writer
	writer.Add("std/cosmo_amd64/fmt.a", []byte("archive of fmt"))
	writer.Add(embedded.IncludeDir+"/textflag.h", []byte("#define NOSPLIT 4\n"))
	writer.Add(embedded.IncludeDir+"/funcdata.h", []byte("#define FUNCDATA_ArgsPointerMaps 0\n"))
	var blob bytes.Buffer
	if _, err := writer.WriteTo(&blob); err != nil {
		t.Fatal(err)
	}
	lead := []byte("the go command's own bytes")
	file := append(append([]byte(nil), lead...), blob.Bytes()...)
	file = append(file, embedded.EncodeTrailer(int64(len(lead)), int64(blob.Len()), sha256.Sum256(blob.Bytes()))...)

	goroot := filepath.Join(t.TempDir(), "go-toolchain")
	if err := os.WriteFile(goroot, file, 0o755); err != nil {
		t.Fatal(err)
	}
	assertHeaders(t, includeHeaders(goroot))
}

// assertHeaders reports the two headers both sources hold, in name order.
func assertHeaders(test *testing.T, headers []header) {
	test.Helper()
	if len(headers) != 2 {
		test.Fatalf("%d headers, not 2: %+v", len(headers), headers)
	}
	if headers[0].name != "funcdata.h" || headers[1].name != "textflag.h" {
		test.Fatalf("the headers are named %q and %q", headers[0].name, headers[1].name)
	}
	if string(headers[0].data) != "#define FUNCDATA_ArgsPointerMaps 0\n" {
		test.Fatalf("funcdata.h reads %q", headers[0].data)
	}
	if string(headers[1].data) != "#define NOSPLIT 4\n" {
		test.Fatalf("textflag.h reads %q", headers[1].data)
	}
}
