// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package embedded

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

// TestBlobRoundTrip writes a blob after some leading bytes, the way the
// linker appends one to an APE, and reads every entry back through the
// trailer.
func TestBlobRoundTrip(t *testing.T) {
	var writer Writer
	writer.Add("std/cosmo_amd64/fmt.a", []byte("archive of fmt"))
	writer.Add("include/textflag.h", []byte("#define NOSPLIT 4\n"))
	writer.Add("manifest/cosmo_amd64", []byte(`{"Target":"cosmo_amd64","Packages":[{"ImportPath":"fmt","Name":"fmt","Imports":["os"],"BuildID":"a/b","Archive":"std/cosmo_amd64/fmt.a"}]}`))
	var blob bytes.Buffer
	if _, err := writer.WriteTo(&blob); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "exe")
	lead := []byte("the executable's own bytes, unaligned in length!")
	file := append(append([]byte(nil), lead...), blob.Bytes()...)
	file = append(file, EncodeTrailer(int64(len(lead)), int64(blob.Len()), sha256.Sum256(blob.Bytes()))...)
	if err := os.WriteFile(path, file, 0o644); err != nil {
		t.Fatal(err)
	}

	opened, err := openBlob(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(opened.entries) != 3 {
		t.Fatalf("the index holds %d entries, not 3", len(opened.entries))
	}
	entry := opened.entries["include/textflag.h"]
	data := make([]byte, entry.Size)
	handle, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if _, err := handle.ReadAt(data, opened.trailer.Offset+entry.Offset); err != nil {
		t.Fatal(err)
	}
	if string(data) != "#define NOSPLIT 4\n" {
		t.Fatalf("read %q for the header", data)
	}
	if entry.Offset%8 != 0 || (opened.trailer.Offset+opened.entries["std/cosmo_amd64/fmt.a"].Offset)%8 == 1 {
		t.Fatalf("entries are not 8-aligned inside the blob: %+v", opened.entries)
	}

	trailer, err := ReadTrailer(handle)
	if err != nil {
		t.Fatal(err)
	}
	if trailer.Sum != sha256.Sum256(blob.Bytes()) {
		t.Fatal("the trailer carries a different hash than the blob")
	}
}

// TestNoTrailer reports a plain file as carrying nothing.
func TestNoTrailer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 200), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openBlob(path); err == nil {
		t.Fatal("a plain file opened as a blob")
	}
}
